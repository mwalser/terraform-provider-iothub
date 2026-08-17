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
	_ action.Action              = &setEdgeModulesAction{}
	_ action.ActionWithConfigure = &setEdgeModulesAction{}
)

// NewSetEdgeModulesAction returns the iothub_set_edge_modules action.
func NewSetEdgeModulesAction() action.Action { return &setEdgeModulesAction{} }

type setEdgeModulesAction struct {
	configured
}

type setEdgeModulesModel struct {
	DeviceID       types.String  `tfsdk:"device_id"`
	ModulesContent jsondoc.Value `tfsdk:"modules_content"`
	Timeout        types.String  `tfsdk:"timeout"`
}

func (a *setEdgeModulesAction) Metadata(_ context.Context, req action.MetadataRequest, resp *action.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_set_edge_modules"
}

func (a *setEdgeModulesAction) Schema(_ context.Context, _ action.SchemaRequest, resp *action.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Sets the modules of one IoT Edge device immediately from a deployment manifest's `modulesContent`. " +
			"This bypasses `iothub_edge_deployment` targeting and is the equivalent of `az iot edge set-modules`. Fails for a " +
			"device that is not an IoT Edge device.\n\n" +
			"Useful for one-off tests on a single gateway. For fleets, use `iothub_edge_deployment`.\n\n" +
			"-> You can build `modules_content` with `provider::iothub::edge_manifest` instead of reading it from a " +
			"file.\n\n" +
			"~> A deployment that targets the device applies its own manifest again the next time the hub evaluates it, for " +
			"example when the deployment is changed.",
		Attributes: map[string]schema.Attribute{
			"device_id": schema.StringAttribute{MarkdownDescription: "ID of the IoT Edge device.", Required: true, Validators: []validator.String{identity.IDValidator()}},
			"modules_content": schema.StringAttribute{
				CustomType:          configuration.ModulesContentType,
				MarkdownDescription: "The `modulesContent` object of a deployment manifest as JSON. It must contain `$edgeAgent`.",
				Required:            true,
			},
			"timeout": timeoutAttribute("10m", "It covers retries of throttled requests."),
		},
	}
}

func (a *setEdgeModulesAction) Invoke(ctx context.Context, req action.InvokeRequest, resp *action.InvokeResponse) {
	var data setEdgeModulesModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := parseTimeout(data.Timeout, defaultActionTimeout)
	resp.Diagnostics.Append(diags...)
	c, d := hubClient(a.pd)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	deviceID := data.DeviceID.ValueString()
	progress(resp, "Setting the modules of edge device %q…", deviceID)
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
