package device

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

// NewCredentialsEphemeral returns the iothub_device_credentials ephemeral resource.
func NewCredentialsEphemeral() ephemeral.EphemeralResource { return &credentialsEphemeral{} }

type credentialsEphemeral struct {
	pd *common.ProviderData
}

type credentialsModel struct {
	DeviceID                  types.String `tfsdk:"device_id"`
	AuthenticationType        types.String `tfsdk:"authentication_type"`
	PrimaryKey                types.String `tfsdk:"primary_key"`
	SecondaryKey              types.String `tfsdk:"secondary_key"`
	PrimaryConnectionString   types.String `tfsdk:"primary_connection_string"`
	SecondaryConnectionString types.String `tfsdk:"secondary_connection_string"`
}

// setUnknown marks every result value as known after apply.
func (m *credentialsModel) setUnknown() {
	m.AuthenticationType = types.StringUnknown()
	m.PrimaryKey, m.SecondaryKey = types.StringUnknown(), types.StringUnknown()
	m.PrimaryConnectionString, m.SecondaryConnectionString = types.StringUnknown(), types.StringUnknown()
}

func (e *credentialsEphemeral) Metadata(_ context.Context, req ephemeral.MetadataRequest, resp *ephemeral.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_device_credentials"
}

func (e *credentialsEphemeral) Schema(_ context.Context, _ ephemeral.SchemaRequest, resp *ephemeral.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The symmetric keys and connection strings of a device. They are never written to state or plan. " +
			"Feed them into write-only arguments such as `azurerm_key_vault_secret.value_wo`. " +
			"Devices with X.509 authentication have no keys, so the key attributes are null for them.\n\n" +
			"Terraform opens ephemeral resources during `plan` as well as `apply`. If the device is created in the same run, " +
			"it does not exist yet at plan time. The plan then shows the values as known after apply and warns that the " +
			"device was not found yet. The values are read during apply.",
		Attributes: map[string]schema.Attribute{
			"device_id":           schema.StringAttribute{MarkdownDescription: "Device ID.", Required: true},
			"authentication_type": schema.StringAttribute{MarkdownDescription: "`sas`, `selfSigned`, `certificateAuthority`, or `none` for identities without credentials such as the hub's system modules.", Computed: true},
			"primary_key":         schema.StringAttribute{MarkdownDescription: "Primary key, base64 encoded. Null unless `authentication_type` is `sas`.", Computed: true, Sensitive: true},
			"secondary_key":       schema.StringAttribute{MarkdownDescription: "Secondary key, base64 encoded. Null unless `authentication_type` is `sas`.", Computed: true, Sensitive: true},
			"primary_connection_string": schema.StringAttribute{
				MarkdownDescription: "`HostName=…;DeviceId=…;SharedAccessKey=<primary key>`. Null unless `authentication_type` is `sas`.",
				Computed:            true, Sensitive: true,
			},
			"secondary_connection_string": schema.StringAttribute{
				MarkdownDescription: "`HostName=…;DeviceId=…;SharedAccessKey=<secondary key>`. Null unless `authentication_type` is `sas`.",
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
	c, ok := common.EphemeralHub(e.pd, req, resp)
	if !ok {
		if resp.Deferred == nil && !resp.Diagnostics.HasError() {
			data.setUnknown()
			resp.Diagnostics.Append(resp.Result.Set(ctx, &data)...)
		}
		return
	}
	dev, err := c.GetDevice(ctx, data.DeviceID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			// Terraform opens ephemeral resources during plan as well. A
			// device that does not exist yet is usually one being created in
			// the same run: defer until apply when Terraform allows it.
			if req.ClientCapabilities.DeferralAllowed {
				tflog.Debug(ctx, "device not found while opening credentials; deferring", map[string]any{"device_id": data.DeviceID.ValueString()})
				resp.Deferred = &ephemeral.Deferred{Reason: ephemeral.DeferredReasonAbsentPrereq}
				return
			}
			// Terraform opens ephemeral resources during plan too, and cannot
			// tell the provider which phase it is in. A device that does not
			// exist yet is normally one created in the same run, so the result
			// is reported as unknown ("known after apply") with a warning; if
			// the device is really missing the warning persists and any
			// write-only consumer fails at apply.
			resp.Diagnostics.AddWarning("Device not found (yet)",
				fmt.Sprintf("No device with ID %q exists in %s at the moment. If it is created in this run, its credentials become "+
					"available at apply time; otherwise check the device ID.", data.DeviceID.ValueString(), c.Hostname()))
			data.setUnknown()
			resp.Diagnostics.Append(resp.Result.Set(ctx, &data)...)
			return
		}
		resp.Diagnostics.AddError("Cannot read IoT Hub device credentials", common.DescribeError(err))
		return
	}
	tflog.Debug(ctx, "opened device credentials", map[string]any{"device_id": dev.DeviceID, "deferral_allowed": req.ClientCapabilities.DeferralAllowed})

	data.AuthenticationType = types.StringNull()
	data.PrimaryKey, data.SecondaryKey = types.StringNull(), types.StringNull()
	data.PrimaryConnectionString, data.SecondaryConnectionString = types.StringNull(), types.StringNull()
	if dev.Authentication != nil {
		data.AuthenticationType = identity.StringOrNull(dev.Authentication.Type)
		if k := dev.Authentication.SymmetricKey; k != nil && dev.Authentication.Type == client.AuthTypeSAS {
			if k.PrimaryKey != "" {
				data.PrimaryKey = types.StringValue(k.PrimaryKey)
				data.PrimaryConnectionString = types.StringValue(client.DeviceConnectionString(c.Hostname(), dev.DeviceID, k.PrimaryKey))
			}
			if k.SecondaryKey != "" {
				data.SecondaryKey = types.StringValue(k.SecondaryKey)
				data.SecondaryConnectionString = types.StringValue(client.DeviceConnectionString(c.Hostname(), dev.DeviceID, k.SecondaryKey))
			}
		}
	}
	resp.Diagnostics.Append(resp.Result.Set(ctx, &data)...)
}
