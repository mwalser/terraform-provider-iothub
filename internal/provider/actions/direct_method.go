package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
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
	_ action.Action                   = &directMethodAction{}
	_ action.ActionWithConfigure      = &directMethodAction{}
	_ action.ActionWithValidateConfig = &directMethodAction{}
)

// NewDirectMethodAction returns the iothub_direct_method action.
func NewDirectMethodAction() action.Action { return &directMethodAction{} }

type directMethodAction struct {
	configured
}

type directMethodModel struct {
	Hostname               types.String `tfsdk:"hostname"`
	DeviceID               types.String `tfsdk:"device_id"`
	ModuleID               types.String `tfsdk:"module_id"`
	MethodName             types.String `tfsdk:"method_name"`
	Payload                types.String `tfsdk:"payload"`
	ResponseTimeoutSeconds types.Int64  `tfsdk:"response_timeout_seconds"`
	ConnectTimeoutSeconds  types.Int64  `tfsdk:"connect_timeout_seconds"`
	ExpectedStatusCodes    types.List   `tfsdk:"expected_status_codes"`
}

const (
	defaultResponseTimeout = 30
	defaultConnectTimeout  = 0
	// maxScheduleAhead is the hub's limit for a scheduled job's start_time
	// (verified: "Must be within 168 hours").
	maxScheduleAhead = 168 * time.Hour
)

func (a *directMethodAction) Metadata(_ context.Context, req action.MetadataRequest, resp *action.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_direct_method"
}

func (a *directMethodAction) Schema(_ context.Context, _ action.SchemaRequest, resp *action.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Invokes a direct method on a device or module and waits for the device's answer. The device-defined " +
			"response status is compared with `expected_status_codes`; anything else fails the apply, as does a device that is " +
			"offline or does not exist. The answer's status and payload are reported as progress output.\n\n" +
			"Trigger it from a resource lifecycle (`action_trigger { events = [after_update] … }`) — e.g. reboot a device after its " +
			"twin changed — or ad hoc with `terraform apply -invoke=action.iothub_direct_method.<name>`. A method invocation is never " +
			"repeated after an ambiguous failure, because it may already have run.",
		Attributes: map[string]schema.Attribute{
			"hostname":    hostnameAttribute(),
			"device_id":   schema.StringAttribute{MarkdownDescription: "Device ID.", Required: true, Validators: []validator.String{identity.IDValidator()}},
			"module_id":   schema.StringAttribute{MarkdownDescription: "Module ID, to call a module's method.", Optional: true, Validators: []validator.String{identity.IDValidator()}},
			"method_name": schema.StringAttribute{MarkdownDescription: "Method name as registered by the device code.", Required: true, Validators: []validator.String{stringvalidator.LengthAtLeast(1)}},
			"payload": schema.StringAttribute{
				MarkdownDescription: "JSON payload (any JSON value; use `jsonencode`). Sent as `null` when omitted.",
				Optional:            true,
			},
			"response_timeout_seconds": schema.Int64Attribute{
				MarkdownDescription: "How long the hub waits for the device's response, 5–300 seconds (default 30).",
				Optional:            true,
				Validators:          []validator.Int64{int64validator.Between(5, 300)},
			},
			"connect_timeout_seconds": schema.Int64Attribute{
				MarkdownDescription: "How long the hub waits for a disconnected device to connect before giving up, 0–300 seconds (default 0).",
				Optional:            true,
				Validators:          []validator.Int64{int64validator.Between(0, 300)},
			},
			"expected_status_codes": schema.ListAttribute{
				MarkdownDescription: "Device-defined response statuses that count as success (default `[200]`). An empty list accepts any status.",
				ElementType:         types.Int64Type,
				Optional:            true,
				Validators:          []validator.List{listvalidator.ValueInt64sAre(int64validator.Between(0, 999))},
			},
		},
	}
}

func (a *directMethodAction) ValidateConfig(ctx context.Context, req action.ValidateConfigRequest, resp *action.ValidateConfigResponse) {
	var data directMethodModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !data.Payload.IsNull() && !data.Payload.IsUnknown() && !json.Valid([]byte(data.Payload.ValueString())) {
		resp.Diagnostics.AddAttributeError(path.Root("payload"), "Invalid payload", "payload must be valid JSON (use jsonencode).")
	}
}

func (a *directMethodAction) Invoke(ctx context.Context, req action.InvokeRequest, resp *action.InvokeResponse) {
	var data directMethodModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, diags := clientFor(ctx, a.pd, data.Hostname)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	mreq := client.MethodRequest{
		MethodName:             data.MethodName.ValueString(),
		ResponseTimeoutSeconds: defaultResponseTimeout,
		ConnectTimeoutSeconds:  defaultConnectTimeout,
	}
	if !data.ResponseTimeoutSeconds.IsNull() {
		mreq.ResponseTimeoutSeconds = data.ResponseTimeoutSeconds.ValueInt64()
	}
	if !data.ConnectTimeoutSeconds.IsNull() {
		mreq.ConnectTimeoutSeconds = data.ConnectTimeoutSeconds.ValueInt64()
	}
	if !data.Payload.IsNull() {
		mreq.Payload = json.RawMessage(data.Payload.ValueString())
	}
	expected := []int64{200}
	if !data.ExpectedStatusCodes.IsNull() {
		expected = nil
		resp.Diagnostics.Append(data.ExpectedStatusCodes.ElementsAs(ctx, &expected, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	deviceID, moduleID := data.DeviceID.ValueString(), data.ModuleID.ValueString()
	target := fmt.Sprintf("device %q", deviceID)
	if moduleID != "" {
		target = fmt.Sprintf("module %q on device %q", moduleID, deviceID)
	}
	progress(resp, "Invoking %s on %s (waiting up to %ds)…", mreq.MethodName, target, mreq.ResponseTimeoutSeconds+mreq.ConnectTimeoutSeconds)
	tflog.Info(ctx, "invoking direct method", map[string]any{"device_id": deviceID, "module_id": moduleID, "method": mreq.MethodName})

	var res *client.MethodResult
	var err error
	if moduleID != "" {
		res, err = c.InvokeModuleMethod(ctx, deviceID, moduleID, mreq)
	} else {
		res, err = c.InvokeDeviceMethod(ctx, deviceID, mreq)
	}
	if err != nil {
		switch {
		case client.IsDeviceNotOnline(err):
			resp.Diagnostics.AddError("Device not online",
				fmt.Sprintf("The %s is not connected to %s, so %q could not be delivered. Raise `connect_timeout_seconds` to wait for it to connect, "+
					"or use a scheduled job (`iothub_scheduled_job`) for offline devices.\n\n%s", target, c.Hostname(), mreq.MethodName, common.DescribeError(err)))
		case client.IsNotFound(err):
			resp.Diagnostics.AddError("Device not found", fmt.Sprintf("The %s does not exist in %s.\n\n%s", target, c.Hostname(), common.DescribeError(err)))
		default:
			resp.Diagnostics.AddError("Direct method failed", common.DescribeError(err))
		}
		return
	}
	payload := compactJSON(res.Payload, 2000)
	ok := len(expected) == 0
	for _, s := range expected {
		if s == res.Status {
			ok = true
		}
	}
	if !ok {
		exp := make([]string, 0, len(expected))
		for _, s := range expected {
			exp = append(exp, fmt.Sprint(s))
		}
		resp.Diagnostics.AddError("Direct method returned an unexpected status",
			fmt.Sprintf("%q on %s answered status %d (expected %s) with payload %s.", mreq.MethodName, target, res.Status, strings.Join(exp, ", "), payload))
		return
	}
	progress(resp, "%s on %s → status %d, payload %s", mreq.MethodName, target, res.Status, payload)
	tflog.Info(ctx, "direct method done", map[string]any{"device_id": deviceID, "module_id": moduleID, "method": mreq.MethodName, "status": res.Status})
}
