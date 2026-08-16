package actions

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/action/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/common"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/identity"
)

var (
	_ action.Action              = &purgeAction{}
	_ action.ActionWithConfigure = &purgeAction{}
)

// NewPurgeC2DQueueAction returns the iothub_purge_c2d_queue action.
func NewPurgeC2DQueueAction() action.Action { return &purgeAction{} }

type purgeAction struct {
	configured
}

type purgeModel struct {
	DeviceID types.String `tfsdk:"device_id"`
	Timeout  types.String `tfsdk:"timeout"`
}

func (a *purgeAction) Metadata(_ context.Context, req action.MetadataRequest, resp *action.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_purge_c2d_queue"
}

func (a *purgeAction) Schema(_ context.Context, _ action.SchemaRequest, resp *action.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Deletes every pending cloud-to-device message of a device and reports how many were purged. Typical " +
			"uses are an `action_trigger` before re-commissioning a device, or an ad hoc `terraform apply -invoke`.",
		Attributes: map[string]schema.Attribute{
			"device_id": schema.StringAttribute{MarkdownDescription: "Device ID.", Required: true, Validators: []validator.String{identity.IDValidator()}},
			"timeout":   timeoutAttribute("10m", "It covers retries of throttled requests."),
		},
	}
}

func (a *purgeAction) Invoke(ctx context.Context, req action.InvokeRequest, resp *action.InvokeResponse) {
	var data purgeModel
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
	res, err := c.PurgeCloudToDeviceQueue(ctx, deviceID)
	if err != nil {
		if client.IsNotFound(err) {
			resp.Diagnostics.AddAttributeError(path.Root("device_id"), "Device not found", fmt.Sprintf("No device with ID %q exists in %s.\n\n%s", deviceID, c.Hostname(), common.DescribeError(err)))
			return
		}
		resp.Diagnostics.AddError("Cannot purge cloud-to-device queue", common.DescribeError(err))
		return
	}
	tflog.Info(ctx, "purged cloud-to-device queue", map[string]any{"device_id": deviceID, "purged": res.TotalMessagesPurged})
	progress(resp, "Purged %d cloud-to-device message(s) queued for %q.", res.TotalMessagesPurged, deviceID)
}
