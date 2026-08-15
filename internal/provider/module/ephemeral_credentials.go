package module

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/common"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/identity"
)

var (
	_ ephemeral.EphemeralResource              = &credentialsEphemeral{}
	_ ephemeral.EphemeralResourceWithConfigure = &credentialsEphemeral{}
)

// NewCredentialsEphemeral returns the iothub_module_credentials ephemeral resource.
func NewCredentialsEphemeral() ephemeral.EphemeralResource { return &credentialsEphemeral{} }

type credentialsEphemeral struct {
	pd *common.ProviderData
}

type credentialsModel struct {
	Hostname                  types.String `tfsdk:"hostname"`
	DeviceID                  types.String `tfsdk:"device_id"`
	ModuleID                  types.String `tfsdk:"module_id"`
	AuthenticationType        types.String `tfsdk:"authentication_type"`
	PrimaryKey                types.String `tfsdk:"primary_key"`
	SecondaryKey              types.String `tfsdk:"secondary_key"`
	PrimaryConnectionString   types.String `tfsdk:"primary_connection_string"`
	SecondaryConnectionString types.String `tfsdk:"secondary_connection_string"`
}

func (e *credentialsEphemeral) Metadata(_ context.Context, req ephemeral.MetadataRequest, resp *ephemeral.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_module_credentials"
}

func (e *credentialsEphemeral) Schema(_ context.Context, _ ephemeral.SchemaRequest, resp *ephemeral.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The symmetric keys and connection strings of a module, read from the identity registry and " +
			"never written to state or plan. Feed them into write-only arguments (e.g. `azurerm_key_vault_secret.value_wo`). " +
			"Modules with X.509 authentication have no keys; the key attributes are then null.\n\n" +
			"Terraform opens ephemeral resources during `plan` as well as `apply`. When the module does not exist yet " +
			"because it is created in the same run, the plan shows the values as known after apply (with a " +
			"\"Module not found (yet)\" warning) and they are read at apply time.",
		Attributes: map[string]schema.Attribute{
			"hostname":            schema.StringAttribute{MarkdownDescription: common.HostnameAttributeDescription, Optional: true, Computed: true},
			"device_id":           schema.StringAttribute{MarkdownDescription: "Device ID.", Required: true},
			"module_id":           schema.StringAttribute{MarkdownDescription: "Module ID.", Required: true},
			"authentication_type": schema.StringAttribute{MarkdownDescription: "`sas`, `selfSigned`, `certificateAuthority` or `none`.", Computed: true},
			"primary_key":         schema.StringAttribute{MarkdownDescription: "Base64 primary key (sas only).", Computed: true, Sensitive: true},
			"secondary_key":       schema.StringAttribute{MarkdownDescription: "Base64 secondary key (sas only).", Computed: true, Sensitive: true},
			"primary_connection_string": schema.StringAttribute{
				MarkdownDescription: "`HostName=…;DeviceId=…;ModuleId=…;SharedAccessKey=<primary key>` (sas only).",
				Computed:            true, Sensitive: true,
			},
			"secondary_connection_string": schema.StringAttribute{
				MarkdownDescription: "`HostName=…;DeviceId=…;ModuleId=…;SharedAccessKey=<secondary key>` (sas only).",
				Computed:            true, Sensitive: true,
			},
		},
	}
}

func (e *credentialsEphemeral) Configure(_ context.Context, req ephemeral.ConfigureRequest, resp *ephemeral.ConfigureResponse) {
	pd, diags := common.ExpectProviderData(req.ProviderData)
	resp.Diagnostics.Append(diags...)
	e.pd = pd
}

func (e *credentialsEphemeral) Open(ctx context.Context, req ephemeral.OpenRequest, resp *ephemeral.OpenResponse) {
	var data credentialsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
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
	mod, err := c.GetModule(ctx, deviceID, moduleID)
	if err != nil {
		if client.IsNotFound(err) {
			// Same reasoning as iothub_device_credentials: Terraform opens
			// ephemeral resources at plan time too, so a missing module is
			// usually one created in this run.
			if req.ClientCapabilities.DeferralAllowed {
				resp.Deferred = &ephemeral.Deferred{Reason: ephemeral.DeferredReasonAbsentPrereq}
				return
			}
			resp.Diagnostics.AddWarning("Module not found (yet)",
				fmt.Sprintf("No module %q on device %q exists in %s at the moment. If it is created in this run, its credentials become "+
					"available at apply time; otherwise check the IDs.", moduleID, deviceID, c.Hostname()))
			data.Hostname = types.StringValue(c.Hostname())
			data.AuthenticationType = types.StringUnknown()
			data.PrimaryKey, data.SecondaryKey = types.StringUnknown(), types.StringUnknown()
			data.PrimaryConnectionString, data.SecondaryConnectionString = types.StringUnknown(), types.StringUnknown()
			resp.Diagnostics.Append(resp.Result.Set(ctx, &data)...)
			return
		}
		resp.Diagnostics.AddError("Cannot read IoT Hub module credentials", common.DescribeError(err))
		return
	}
	tflog.Debug(ctx, "opened module credentials", map[string]any{"device_id": deviceID, "module_id": moduleID})

	data.Hostname = types.StringValue(c.Hostname())
	data.AuthenticationType = types.StringNull()
	data.PrimaryKey, data.SecondaryKey = types.StringNull(), types.StringNull()
	data.PrimaryConnectionString, data.SecondaryConnectionString = types.StringNull(), types.StringNull()
	if mod.Authentication != nil {
		data.AuthenticationType = identity.StringOrNull(mod.Authentication.Type)
		if k := mod.Authentication.SymmetricKey; k != nil && mod.Authentication.Type == client.AuthTypeSAS {
			if k.PrimaryKey != "" {
				data.PrimaryKey = types.StringValue(k.PrimaryKey)
				data.PrimaryConnectionString = types.StringValue(client.ModuleConnectionString(c.Hostname(), mod.DeviceID, mod.ModuleID, k.PrimaryKey))
			}
			if k.SecondaryKey != "" {
				data.SecondaryKey = types.StringValue(k.SecondaryKey)
				data.SecondaryConnectionString = types.StringValue(client.ModuleConnectionString(c.Hostname(), mod.DeviceID, mod.ModuleID, k.SecondaryKey))
			}
		}
	}
	resp.Diagnostics.Append(resp.Result.Set(ctx, &data)...)
}
