package device

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/common"
)

var (
	_ ephemeral.EphemeralResource                   = &sasTokenEphemeral{}
	_ ephemeral.EphemeralResourceWithConfigure      = &sasTokenEphemeral{}
	_ ephemeral.EphemeralResourceWithValidateConfig = &sasTokenEphemeral{}
)

// NewSASTokenEphemeral returns the iothub_device_sas_token ephemeral resource.
func NewSASTokenEphemeral() ephemeral.EphemeralResource { return &sasTokenEphemeral{} }

type sasTokenEphemeral struct {
	pd  *common.ProviderData
	now func() time.Time
}

type sasTokenModel struct {
	Hostname    types.String `tfsdk:"hostname"`
	DeviceID    types.String `tfsdk:"device_id"`
	ModuleID    types.String `tfsdk:"module_id"`
	TTL         types.String `tfsdk:"ttl"`
	Key         types.String `tfsdk:"key"`
	Token       types.String `tfsdk:"token"`
	ExpiresAt   types.String `tfsdk:"expires_at"`
	ResourceURI types.String `tfsdk:"resource_uri"`
}

const (
	defaultTokenTTL = time.Hour
	keyPrimary      = "primary"
	keySecondary    = "secondary"
)

func (e *sasTokenEphemeral) Metadata(_ context.Context, req ephemeral.MetadataRequest, resp *ephemeral.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_device_sas_token"
}

func (e *sasTokenEphemeral) Schema(_ context.Context, _ ephemeral.SchemaRequest, resp *ephemeral.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A shared access signature for a device or module (`SharedAccessSignature sr=…&sig=…&se=…`), " +
			"signed with the identity's symmetric key. It is never written to state or plan. Hand it to a write-only argument or " +
			"to a provisioning step that needs a short-lived device credential without exposing the key itself.\n\n" +
			"As with `iothub_device_credentials`, an identity that is created in the same run yields unknown values at plan " +
			"time and the real token at apply.",
		Attributes: map[string]schema.Attribute{
			"hostname":  schema.StringAttribute{MarkdownDescription: common.HostnameAttributeDescription, Optional: true, Computed: true, Validators: common.HostnameValidators()},
			"device_id": schema.StringAttribute{MarkdownDescription: "Device ID.", Required: true},
			"module_id": schema.StringAttribute{MarkdownDescription: "Module ID, for a module token.", Optional: true},
			"ttl": schema.StringAttribute{
				MarkdownDescription: "Token lifetime as a Go duration such as `30m`, `24h` or `168h` (default `1h`). Counted from the moment the token is minted.",
				Optional:            true,
			},
			"key": schema.StringAttribute{
				MarkdownDescription: "Which key signs the token: `primary` (default) or `secondary`.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.OneOf(keyPrimary, keySecondary)},
			},
			"token":        schema.StringAttribute{MarkdownDescription: "The `SharedAccessSignature …` token.", Computed: true, Sensitive: true},
			"expires_at":   schema.StringAttribute{MarkdownDescription: "Expiry time in RFC 3339 format (UTC).", Computed: true},
			"resource_uri": schema.StringAttribute{MarkdownDescription: "The `sr` the token was signed for.", Computed: true},
		},
	}
}

func (e *sasTokenEphemeral) Configure(_ context.Context, req ephemeral.ConfigureRequest, resp *ephemeral.ConfigureResponse) {
	pd, diags := common.ExpectProviderData(req.ProviderData)
	resp.Diagnostics.Append(diags...)
	e.pd = pd
}

func (e *sasTokenEphemeral) ValidateConfig(ctx context.Context, req ephemeral.ValidateConfigRequest, resp *ephemeral.ValidateConfigResponse) {
	var data sasTokenModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if _, errs := parseTTL(data.TTL); len(errs) > 0 {
		resp.Diagnostics.AddAttributeError(path.Root("ttl"), "Invalid ttl", errs[0].Error()+" — use a Go duration such as \"30m\" or \"24h\".")
	}
}

func parseTTL(v types.String) (time.Duration, []error) {
	if v.IsNull() || v.IsUnknown() {
		return defaultTokenTTL, nil
	}
	d, err := time.ParseDuration(v.ValueString())
	if err != nil {
		return 0, []error{err}
	}
	if d <= 0 {
		return 0, []error{fmt.Errorf("must be positive")}
	}
	return d, nil
}

func (e *sasTokenEphemeral) Open(ctx context.Context, req ephemeral.OpenRequest, resp *ephemeral.OpenResponse) {
	var data sasTokenModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ttl, errs := parseTTL(data.TTL)
	if len(errs) > 0 {
		resp.Diagnostics.AddAttributeError(path.Root("ttl"), "Invalid ttl", errs[0].Error()+" — use a Go duration such as \"30m\" or \"24h\".")
		return
	}
	hostname, ok, diags := common.ResolveHostname(data.Hostname, e.pd)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !ok {
		if req.ClientCapabilities.DeferralAllowed {
			resp.Deferred = &ephemeral.Deferred{Reason: ephemeral.DeferredReasonProviderConfigUnknown}
		}
		return
	}
	c, diags := e.pd.ClientFor(ctx, hostname)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	deviceID, moduleID := data.DeviceID.ValueString(), data.ModuleID.ValueString()
	resourceURI := c.Hostname() + "/devices/" + deviceID
	what := fmt.Sprintf("device %q", deviceID)
	var auth *client.AuthenticationMechanism
	var err error
	if moduleID != "" {
		resourceURI += "/modules/" + moduleID
		what = fmt.Sprintf("module %q on device %q", moduleID, deviceID)
		var mod *client.Module
		if mod, err = c.GetModule(ctx, deviceID, moduleID); err == nil {
			auth = mod.Authentication
		}
	} else {
		var dev *client.Device
		if dev, err = c.GetDevice(ctx, deviceID); err == nil {
			auth = dev.Authentication
		}
	}
	if err != nil {
		if client.IsNotFound(err) {
			if req.ClientCapabilities.DeferralAllowed {
				resp.Deferred = &ephemeral.Deferred{Reason: ephemeral.DeferredReasonAbsentPrereq}
				return
			}
			resp.Diagnostics.AddWarning("Identity not found (yet)",
				fmt.Sprintf("No %s exists in %s at the moment. If it is created in this run, the token becomes available at apply time; otherwise check the IDs.", what, c.Hostname()))
			data.Hostname = types.StringValue(c.Hostname())
			data.ResourceURI = types.StringValue(resourceURI)
			data.Token, data.ExpiresAt = types.StringUnknown(), types.StringUnknown()
			resp.Diagnostics.Append(resp.Result.Set(ctx, &data)...)
			return
		}
		resp.Diagnostics.AddError("Cannot read IoT Hub identity for the SAS token", common.DescribeError(err))
		return
	}
	if auth == nil || auth.Type != client.AuthTypeSAS || auth.SymmetricKey == nil {
		authType := "unknown"
		if auth != nil {
			authType = auth.Type
		}
		resp.Diagnostics.AddAttributeError(path.Root("device_id"), "No symmetric key to sign with",
			fmt.Sprintf("The %s authenticates with %q; SAS tokens can only be minted for identities with `sas` authentication.", what, authType))
		return
	}
	key, which := auth.SymmetricKey.PrimaryKey, keyPrimary
	if data.Key.ValueString() == keySecondary {
		key, which = auth.SymmetricKey.SecondaryKey, keySecondary
	}
	if key == "" {
		resp.Diagnostics.AddAttributeError(path.Root("key"), "Key not set", fmt.Sprintf("The %s has no %s key.", what, which))
		return
	}
	now := time.Now
	if e.now != nil {
		now = e.now
	}
	expiry := now().Add(ttl).UTC().Truncate(time.Second)
	token, err := client.SASToken(resourceURI, "", key, expiry)
	if err != nil {
		resp.Diagnostics.AddError("Cannot mint SAS token", err.Error())
		return
	}
	tflog.Debug(ctx, "minted device SAS token", map[string]any{"resource_uri": resourceURI, "expires_at": expiry.Format(time.RFC3339)})
	data.Hostname = types.StringValue(c.Hostname())
	data.ResourceURI = types.StringValue(resourceURI)
	data.Token = types.StringValue(token)
	data.ExpiresAt = types.StringValue(expiry.Format(time.RFC3339))
	resp.Diagnostics.Append(resp.Result.Set(ctx, &data)...)
}
