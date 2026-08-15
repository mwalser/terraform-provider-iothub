package actions

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/action/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/common"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/configuration"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/identity"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/jsondoc"
)

var (
	_ action.Action              = &applyConfigurationAction{}
	_ action.ActionWithConfigure = &applyConfigurationAction{}
)

// NewApplyConfigurationAction returns the iothub_apply_configuration action.
func NewApplyConfigurationAction() action.Action { return &applyConfigurationAction{} }

type applyConfigurationAction struct {
	configured
}

type applyConfigurationModel struct {
	Hostname       types.String  `tfsdk:"hostname"`
	DeviceID       types.String  `tfsdk:"device_id"`
	ModulesContent jsondoc.Value `tfsdk:"modules_content"`
}

func (a *applyConfigurationAction) Metadata(_ context.Context, req action.MetadataRequest, resp *action.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_apply_configuration"
}

func (a *applyConfigurationAction) Schema(_ context.Context, _ action.SchemaRequest, resp *action.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Applies a deployment manifest's `modulesContent` to one IoT Edge device immediately, bypassing " +
			"`iothub_edge_deployment` targeting — the equivalent of `az iot edge set-modules`. Useful for one-off tests on a single " +
			"gateway; fleets should use `iothub_edge_deployment`. Fails for a device that is not an IoT Edge device.",
		Attributes: map[string]schema.Attribute{
			"hostname":  hostnameAttribute(),
			"device_id": schema.StringAttribute{MarkdownDescription: "ID of the IoT Edge device.", Required: true, Validators: []validator.String{identity.IDValidator()}},
			"modules_content": schema.StringAttribute{
				CustomType:          configuration.ModulesContentType,
				MarkdownDescription: "The `modulesContent` object of a deployment manifest as JSON (must contain `$edgeAgent`).",
				Required:            true,
			},
		},
	}
}

func (a *applyConfigurationAction) Invoke(ctx context.Context, req action.InvokeRequest, resp *action.InvokeResponse) {
	var data applyConfigurationModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, diags := clientFor(ctx, a.pd, data.Hostname)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	deviceID := data.DeviceID.ValueString()
	progress(resp, "Applying modules content to edge device %q…", deviceID)
	tflog.Info(ctx, "applying configuration content", map[string]any{"device_id": deviceID})
	if err := c.ApplyConfigurationContent(ctx, deviceID, json.RawMessage(data.ModulesContent.ValueString())); err != nil {
		switch {
		case client.IsNotFound(err):
			resp.Diagnostics.AddAttributeError(path.Root("device_id"), "Device not found", fmt.Sprintf("No device with ID %q exists in %s.\n\n%s", deviceID, c.Hostname(), common.DescribeError(err)))
		default:
			resp.Diagnostics.AddError("Cannot apply configuration content", common.DescribeError(err))
		}
		return
	}
	progress(resp, "Modules content applied to %q; the edge agent picks it up on its next twin update.", deviceID)
}
