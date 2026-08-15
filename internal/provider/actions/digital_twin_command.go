package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

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
	_ action.Action                   = &digitalTwinCommandAction{}
	_ action.ActionWithConfigure      = &digitalTwinCommandAction{}
	_ action.ActionWithValidateConfig = &digitalTwinCommandAction{}
)

// NewDigitalTwinCommandAction returns the iothub_digital_twin_command action.
func NewDigitalTwinCommandAction() action.Action { return &digitalTwinCommandAction{} }

type digitalTwinCommandAction struct {
	configured
}

type digitalTwinCommandModel struct {
	Hostname               types.String `tfsdk:"hostname"`
	DigitalTwinID          types.String `tfsdk:"digital_twin_id"`
	ComponentPath          types.String `tfsdk:"component_path"`
	CommandName            types.String `tfsdk:"command_name"`
	Payload                types.String `tfsdk:"payload"`
	ResponseTimeoutSeconds types.Int64  `tfsdk:"response_timeout_seconds"`
	ConnectTimeoutSeconds  types.Int64  `tfsdk:"connect_timeout_seconds"`
	ExpectedStatusCodes    types.List   `tfsdk:"expected_status_codes"`
}

// dtdlNamePattern is the DTDL name grammar for components and commands
// (letters, digits, underscore; starts with a letter; ≤64 characters).
var dtdlNamePattern = regexp.MustCompile(`^[A-Za-z](?:[A-Za-z0-9_]{0,62}[A-Za-z0-9])?$`)

func (a *digitalTwinCommandAction) Metadata(_ context.Context, req action.MetadataRequest, resp *action.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_digital_twin_command"
}

func (a *digitalTwinCommandAction) Schema(_ context.Context, _ action.SchemaRequest, resp *action.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Invokes an IoT Plug and Play command on a device and waits for the device's answer. Set " +
			"`component_path` for a component command. Leave it out for a root-level command. The device's response status is " +
			"compared with `expected_status_codes`. Any other status fails the apply. So does a device that is offline or does not " +
			"exist. The device's status and payload are shown in the apply output.\n\n" +
			"If the hub's answer is lost, for example on a network timeout, the action fails without retrying, because the command " +
			"may already have run on the device.\n\n" +
			"~> **Requires SAS authentication.** IoT Hub does not accept Entra ID tokens for Plug and Play commands, whatever the " +
			"caller's role. Configure the provider with a `connection_string` whose policy has *ServiceConnect*, for example " +
			"`service` or `iothubowner`. Under Entra ID, call the equivalent direct method with `iothub_direct_method` and " +
			"`method_name = \"<component>*<command>\"`.",
		Attributes: map[string]schema.Attribute{
			"hostname":        hostnameAttribute(),
			"digital_twin_id": schema.StringAttribute{MarkdownDescription: "The device ID. A digital twin has the same ID as its device.", Required: true, Validators: []validator.String{identity.IDValidator()}},
			"component_path": schema.StringAttribute{
				MarkdownDescription: "Component name for a component command. Omit it for a root-level command.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.RegexMatches(dtdlNamePattern, "must be a DTDL name (letters, digits and underscores, starting with a letter, at most 64 characters)")},
			},
			"command_name": schema.StringAttribute{
				MarkdownDescription: "Command name as declared in the device's DTDL model.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.RegexMatches(dtdlNamePattern, "must be a DTDL name (letters, digits and underscores, starting with a letter, at most 64 characters)")},
			},
			"payload": schema.StringAttribute{
				MarkdownDescription: "JSON request payload, any JSON value (use `jsonencode`). Sent as `null` when omitted.",
				Optional:            true,
			},
			"response_timeout_seconds": schema.Int64Attribute{
				MarkdownDescription: "How long the hub waits for the device's response, 5 to 300 seconds (default 30).",
				Optional:            true,
				Validators:          []validator.Int64{int64validator.Between(5, 300)},
			},
			"connect_timeout_seconds": schema.Int64Attribute{
				MarkdownDescription: "How long the hub waits for a disconnected device to connect before giving up, 0 to 300 seconds (default 0).",
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

func (a *digitalTwinCommandAction) ValidateConfig(ctx context.Context, req action.ValidateConfigRequest, resp *action.ValidateConfigResponse) {
	var data digitalTwinCommandModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !data.Payload.IsNull() && !data.Payload.IsUnknown() && !json.Valid([]byte(data.Payload.ValueString())) {
		resp.Diagnostics.AddAttributeError(path.Root("payload"), "Invalid payload", "payload must be valid JSON (use jsonencode).")
	}
}

func (a *digitalTwinCommandAction) Invoke(ctx context.Context, req action.InvokeRequest, resp *action.InvokeResponse) {
	var data digitalTwinCommandModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, diags := clientFor(ctx, a.pd, data.Hostname)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	cmd := client.DigitalTwinCommand{
		DigitalTwinID:          data.DigitalTwinID.ValueString(),
		ComponentPath:          data.ComponentPath.ValueString(),
		CommandName:            data.CommandName.ValueString(),
		ResponseTimeoutSeconds: defaultResponseTimeout,
		ConnectTimeoutSeconds:  defaultConnectTimeout,
	}
	if !data.ResponseTimeoutSeconds.IsNull() {
		cmd.ResponseTimeoutSeconds = data.ResponseTimeoutSeconds.ValueInt64()
	}
	if !data.ConnectTimeoutSeconds.IsNull() {
		cmd.ConnectTimeoutSeconds = data.ConnectTimeoutSeconds.ValueInt64()
	}
	if !data.Payload.IsNull() {
		cmd.Payload = json.RawMessage(data.Payload.ValueString())
	}
	expected := []int64{200}
	if !data.ExpectedStatusCodes.IsNull() {
		expected = nil
		resp.Diagnostics.Append(data.ExpectedStatusCodes.ElementsAs(ctx, &expected, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	name := cmd.CommandName
	if cmd.ComponentPath != "" {
		name = cmd.ComponentPath + "*" + cmd.CommandName
	}
	target := fmt.Sprintf("device %q", cmd.DigitalTwinID)
	progress(resp, "Invoking command %s on %s (waiting up to %ds)…", name, target, cmd.ResponseTimeoutSeconds+cmd.ConnectTimeoutSeconds)
	tflog.Info(ctx, "invoking digital twin command", map[string]any{"device_id": cmd.DigitalTwinID, "component": cmd.ComponentPath, "command": cmd.CommandName})

	res, err := c.InvokeDigitalTwinCommand(ctx, cmd)
	if err != nil {
		switch {
		case client.IsDeviceNotOnline(err):
			resp.Diagnostics.AddError("Device not online",
				fmt.Sprintf("The %s is not connected to %s, so command %s could not be delivered. Raise `connect_timeout_seconds` to wait for it to connect.\n\n%s",
					target, c.Hostname(), name, common.DescribeError(err)))
		case client.IsNotFound(err):
			resp.Diagnostics.AddError("Device not found", fmt.Sprintf("The %s does not exist in %s.\n\n%s", target, c.Hostname(), common.DescribeError(err)))
		case client.IsUnauthorized(err) && a.pd != nil && a.pd.Settings.Mode == common.AuthEntraID:
			resp.Diagnostics.AddError("Digital twin commands need SAS authentication",
				fmt.Sprintf("%s rejected the command with %s. The digital twin command endpoint does not accept Entra ID tokens "+
					"(verified against the service; the role assignment is not the problem). Configure the provider with "+
					"`connection_string` (a shared access policy with ServiceConnect) for this action, or invoke the equivalent direct "+
					"method with `iothub_direct_method` (`method_name = %q`), which works with Entra ID.\n\n%s",
					c.Hostname(), describeStatus(err), name, err.Error()))
		case client.IsUnauthorized(err):
			resp.Diagnostics.AddError("Not authorized to invoke digital twin commands",
				fmt.Sprintf("The shared access policy needs the ServiceConnect permission (`service` or `iothubowner`).\n\n%s", err.Error()))
		default:
			resp.Diagnostics.AddError("Digital twin command failed", common.DescribeError(err))
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
		resp.Diagnostics.AddError("Digital twin command returned an unexpected status",
			fmt.Sprintf("Command %s on %s answered status %d (expected %s) with payload %s.", name, target, res.Status, strings.Join(exp, ", "), payload))
		return
	}
	progress(resp, "Command %s on %s → status %d, payload %s", name, target, res.Status, payload)
	tflog.Info(ctx, "digital twin command done", map[string]any{"device_id": cmd.DigitalTwinID, "command": name, "status": res.Status})
}

// describeStatus renders "HTTP <status> <code>" for an API error.
func describeStatus(err error) string {
	if e, ok := client.AsError(err); ok {
		return strings.TrimSpace(fmt.Sprintf("HTTP %d %s", e.StatusCode, e.Code))
	}
	return err.Error()
}
