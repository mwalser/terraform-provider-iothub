package device

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/identityschema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/common"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/identity"
)

var (
	_ resource.Resource                   = &deviceResource{}
	_ resource.ResourceWithConfigure      = &deviceResource{}
	_ resource.ResourceWithImportState    = &deviceResource{}
	_ resource.ResourceWithModifyPlan     = &deviceResource{}
	_ resource.ResourceWithValidateConfig = &deviceResource{}
	_ resource.ResourceWithIdentity       = &deviceResource{}
)

// NewResource returns the iothub_device resource.
func NewResource() resource.Resource { return &deviceResource{} }

type deviceResource struct {
	pd *common.ProviderData
}

type resourceModel struct {
	ID                         types.String   `tfsdk:"id"`
	DeviceID                   types.String   `tfsdk:"device_id"`
	Status                     types.String   `tfsdk:"status"`
	StatusReason               types.String   `tfsdk:"status_reason"`
	EdgeEnabled                types.Bool     `tfsdk:"edge_enabled"`
	ParentScope                types.String   `tfsdk:"parent_scope"`
	Authentication             types.Object   `tfsdk:"authentication"`
	PrimaryKeyWO               types.String   `tfsdk:"primary_key_wo"`
	PrimaryKeyWOVersion        types.Int64    `tfsdk:"primary_key_wo_version"`
	SecondaryKeyWO             types.String   `tfsdk:"secondary_key_wo"`
	SecondaryKeyWOVersion      types.Int64    `tfsdk:"secondary_key_wo_version"`
	ETag                       types.String   `tfsdk:"etag"`
	GenerationID               types.String   `tfsdk:"generation_id"`
	DeviceScope                types.String   `tfsdk:"device_scope"`
	ConnectionState            types.String   `tfsdk:"connection_state"`
	ConnectionStateUpdatedTime types.String   `tfsdk:"connection_state_updated_time"`
	LastActivityTime           types.String   `tfsdk:"last_activity_time"`
	StatusUpdatedTime          types.String   `tfsdk:"status_updated_time"`
	CloudToDeviceMessageCount  types.Int64    `tfsdk:"cloud_to_device_message_count"`
	Timeouts                   timeouts.Value `tfsdk:"timeouts"`
}

const (
	defaultCreateTimeout = 20 * time.Minute
	defaultReadTimeout   = 20 * time.Minute
	defaultUpdateTimeout = 20 * time.Minute
	defaultDeleteTimeout = 20 * time.Minute
)

func (r *deviceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_device"
}

func (r *deviceResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A device identity in the IoT Hub identity registry.\n\n" +
			"Creating a device also creates its twin. Manage tags and desired properties with `iothub_device_twin`. " +
			"Deleting a device deletes its twin and its modules. Only `device_id` forces replacement. Every " +
			"other attribute changes in place.\n\n" +
			"**Credentials.** With `authentication.type = \"sas\"` and no keys given, the hub generates the keys. They are " +
			"stored in state as sensitive values. To keep keys out of state, pass them through the write-only " +
			"`primary_key_wo` and `secondary_key_wo` arguments and read connection strings with the `iothub_device_credentials` " +
			"ephemeral resource. To rotate a write-only key, change the matching `*_wo_version`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The device ID. Also the import ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"device_id": schema.StringAttribute{
				MarkdownDescription: "Device ID: " + identity.IDDescription + ". Changing it replaces the device.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{identity.IDValidator()},
			},
			"status": schema.StringAttribute{
				MarkdownDescription: "`enabled` (default) or `disabled`. A disabled device cannot connect.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(client.StatusEnabled),
				Validators:          []validator.String{stringvalidator.OneOf(client.StatusEnabled, client.StatusDisabled)},
			},
			"status_reason": schema.StringAttribute{
				MarkdownDescription: "Free-text reason for the status, up to 128 characters.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.LengthAtMost(128)},
			},
			"edge_enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether the device is an IoT Edge device (default `false`). Edge devices get a hub-generated " +
					"`device_scope` and the `$edgeAgent` and `$edgeHub` module identities.",
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(false),
			},
			"parent_scope": schema.StringAttribute{
				MarkdownDescription: "The `device_scope` of the parent IoT Edge device. Setting it makes this device a child of " +
					"that gateway, either as a leaf device or as a nested edge device. Remove it to detach the device from its " +
					"parent. A device has at most one parent.",
				Optional:   true,
				Validators: []validator.String{stringvalidator.RegexMatches(parentScopePattern, "must be an edge device scope starting with "+parentScopePrefix)},
			},
			"authentication": identity.AuthAttribute("device"),
			// ---- computed ----
			"etag":                          computedString("ETag of the identity."),
			"generation_id":                 computedStringStable("Hub-generated ID that changes when a device with the same `device_id` is re-created."),
			"device_scope":                  computedString("The device's own scope. Hub-generated for edge devices, the parent's scope for child leaf devices, otherwise empty."),
			"connection_state":              computedString("`Connected` or `Disconnected`. Updated by the service and approximate."),
			"connection_state_updated_time": computedString("When the connection state last changed."),
			"last_activity_time":            computedString("Last time the device connected, sent or received a message."),
			"status_updated_time":           computedString("When `status` last changed."),
			"cloud_to_device_message_count": schema.Int64Attribute{
				MarkdownDescription: "Number of cloud-to-device messages queued for the device.",
				Computed:            true,
			},
		},
		Blocks: map[string]schema.Block{
			"timeouts": timeouts.Block(ctx, common.TimeoutsOpts("20m")),
		},
	}
	for name, a := range identity.WriteOnlyKeyAttributes() {
		resp.Schema.Attributes[name] = a
	}
}

// identityModel is the resource identity: the device ID.
type identityModel struct {
	DeviceID types.String `tfsdk:"device_id"`
}

func (r *deviceResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	resp.IdentitySchema = identityschema.Schema{
		Attributes: map[string]identityschema.Attribute{
			"device_id": identityschema.StringAttribute{RequiredForImport: true, Description: "Device ID."},
		},
	}
}

func setIdentity(ctx context.Context, id *tfsdk.ResourceIdentity, deviceID string) diag.Diagnostics {
	if id == nil {
		return nil
	}
	return id.Set(ctx, &identityModel{DeviceID: types.StringValue(deviceID)})
}

func computedString(desc string) schema.StringAttribute {
	return schema.StringAttribute{MarkdownDescription: desc, Computed: true}
}

func computedStringStable(desc string) schema.StringAttribute {
	return schema.StringAttribute{MarkdownDescription: desc, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}}
}

func (r *deviceResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	pd, diags := common.ExpectProviderData(req.ProviderData)
	resp.Diagnostics.Append(diags...)
	r.pd = pd
}

// ValidateConfig enforces the cross-attribute rules of the authentication
// block.
func (r *deviceResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var data resourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(identity.ValidateAuth(ctx, data.Authentication, data.writeOnlyKeys())...)
}

// ModifyPlan defers when the hub is not known yet, sets the ID, and keeps
// the planned authentication object consistent with its type so the apply
// never produces a "inconsistent result" (CONCEPT.md §6.1).
func (r *deviceResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() { // destroy
		return
	}
	var plan, config resourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var state *resourceModel
	if !req.State.Raw.IsNull() {
		state = &resourceModel{}
		resp.Diagnostics.Append(req.State.Get(ctx, state)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	common.DeferIfHubUnknown(r.pd, req, resp)
	if !plan.DeviceID.IsUnknown() {
		plan.ID = plan.DeviceID
	}

	// authentication: make the planned object consistent with the type.
	stateAuth := types.ObjectNull(identity.AuthAttrTypes)
	if state != nil {
		stateAuth = state.Authentication
	}
	auth, diags := identity.PlanAuth(ctx, config.Authentication, stateAuth, config.writeOnlyKeys())
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.Authentication = auth
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *deviceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan, config resourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := plan.Timeouts.Create(ctx, defaultCreateTimeout)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	c, diags := r.pd.HubOrError()
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	hostname := c.Hostname()

	spec, diags := r.spec(ctx, plan, config, nil)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "creating IoT Hub device", map[string]any{"hostname": hostname, "device_id": spec.DeviceID})
	created, err := c.CreateDevice(ctx, spec)
	if err != nil {
		if client.IsConflict(err) {
			resp.Diagnostics.AddAttributeError(path.Root("device_id"), "Device already exists",
				fmt.Sprintf("A device with ID %q already exists in %s. To manage it with Terraform, import it:\n\n  terraform import <address> %s\n\n%s",
					spec.DeviceID, hostname, spec.DeviceID, common.DescribeError(err)))
			return
		}
		resp.Diagnostics.AddError("Cannot create IoT Hub device", common.DescribeError(err))
		return
	}
	pk, sk := plan.keysInState()
	setState(&plan, created, pk, sk)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	resp.Diagnostics.Append(setIdentity(ctx, resp.Identity, created.DeviceID)...)
}

func (r *deviceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state resourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := state.Timeouts.Read(ctx, defaultReadTimeout)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	c, ok, diags := r.pd.Hub()
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !ok {
		// The hub is not known yet (first plan of a configuration that also
		// creates it); the prior state stands until apply.
		resp.Diagnostics.Append(setIdentity(ctx, resp.Identity, state.DeviceID.ValueString())...)
		return
	}
	// ETag-gated refresh (CONCEPT.md §11.2): the twin is cheap to read and its
	// deviceEtag is the identity ETag; when it matches, only the volatile
	// fields need refreshing and the registry read is skipped.
	if tw := r.pd.Refresh.TwinIfUnchanged(ctx, func(ctx context.Context) (*client.Twin, error) {
		return c.GetDeviceTwin(ctx, state.DeviceID.ValueString())
	}, state.ETag.ValueString(), state.ConnectionState.ValueString()); tw != nil {
		state.LastActivityTime = identity.TimeOrNull(tw.LastActivityTime)
		state.CloudToDeviceMessageCount = types.Int64Value(tw.CloudToDeviceMessageCount)
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		resp.Diagnostics.Append(setIdentity(ctx, resp.Identity, state.DeviceID.ValueString())...)
		return
	}
	dev, err := c.GetDevice(ctx, state.DeviceID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			tflog.Info(ctx, "IoT Hub device no longer exists; removing from state", map[string]any{"device_id": state.DeviceID.ValueString()})
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Cannot read IoT Hub device", common.DescribeError(err))
		return
	}
	pk, sk := state.keysInState()
	setState(&state, dev, pk, sk)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	resp.Diagnostics.Append(setIdentity(ctx, resp.Identity, dev.DeviceID)...)
}

func (r *deviceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, config, state resourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := plan.Timeouts.Update(ctx, defaultUpdateTimeout)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	c, diags := r.pd.HubOrError()
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := state.DeviceID.ValueString()

	// Re-read right before the full-body PUT: it yields the current keys
	// (needed with write-only keys, which are not in state) and the fresh
	// ETag; a changed ETag triggers conflict inspection (CONCEPT.md §11.1).
	var updated *client.Device
	for attempt := 1; ; attempt++ {
		fresh, err := c.GetDevice(ctx, id)
		if err != nil {
			if client.IsNotFound(err) {
				resp.Diagnostics.AddError("Device no longer exists",
					fmt.Sprintf("Device %q was deleted outside Terraform. Run `terraform plan` again; it will be re-created.", id))
				return
			}
			resp.Diagnostics.AddError("Cannot read IoT Hub device before update", common.DescribeError(err))
			return
		}
		if fresh.ETag != state.ETag.ValueString() {
			priorAuth, _, d := identity.AuthFromObject(ctx, state.Authentication)
			resp.Diagnostics.Append(d...)
			if changed := diffWritten(writtenFromState(state, priorAuth), writtenFromHub(fresh)); len(changed) > 0 {
				resp.Diagnostics.AddError("Device changed outside Terraform",
					fmt.Sprintf("Device %q was modified since the last refresh:\n  - %s\n\nRun `terraform plan` again to review the external change before applying.", id, strings.Join(changed, "\n  - ")))
				return
			}
			tflog.Debug(ctx, "identity ETag moved without a change to written fields; continuing", map[string]any{"device_id": id})
		}

		spec, d := r.spec(ctx, plan, config, fresh)
		resp.Diagnostics.Append(d...)
		if resp.Diagnostics.HasError() {
			return
		}
		currentIsEdge := fresh.Capabilities != nil && fresh.Capabilities.IotEdge
		if spec.IotEdge && currentIsEdge {
			spec.OwnDeviceScope = fresh.DeviceScope // edge devices must echo their scope
		}
		// An edge device becoming a leaf loses its parent in the same PUT
		// (verified); detach first, then attach in a second write.
		twoStep := currentIsEdge && !spec.IotEdge && spec.ParentScope != ""
		if twoStep {
			first := spec
			first.ParentScope = ""
			step1, err := c.UpdateDevice(ctx, first, fresh.ETag)
			if err != nil {
				if client.IsPreconditionFailed(err) && attempt < 3 {
					continue
				}
				resp.Diagnostics.AddError("Cannot update IoT Hub device", common.DescribeError(err))
				return
			}
			fresh.ETag = step1.ETag
		}
		updated, err = c.UpdateDevice(ctx, spec, fresh.ETag)
		if err == nil {
			break
		}
		if client.IsPreconditionFailed(err) && attempt < 3 {
			tflog.Debug(ctx, "412 on update; re-reading and retrying", map[string]any{"device_id": id, "attempt": attempt})
			continue
		}
		resp.Diagnostics.AddError("Cannot update IoT Hub device", common.DescribeError(err))
		return
	}

	pk, sk := plan.keysInState()
	setState(&plan, updated, pk, sk)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	resp.Diagnostics.Append(setIdentity(ctx, resp.Identity, updated.DeviceID)...)
}

func (r *deviceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state resourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := state.Timeouts.Delete(ctx, defaultDeleteTimeout)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	c, diags := r.pd.HubOrError()
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := c.DeleteDevice(ctx, state.DeviceID.ValueString(), "*"); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Cannot delete IoT Hub device", common.DescribeError(err))
	}
}

func (r *deviceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	deviceID := strings.TrimSpace(req.ID)
	if deviceID == "" && req.Identity != nil {
		var id identityModel
		resp.Diagnostics.Append(req.Identity.Get(ctx, &id)...)
		if resp.Diagnostics.HasError() {
			return
		}
		deviceID = id.DeviceID.ValueString()
	}
	if deviceID == "" {
		resp.Diagnostics.AddError("Invalid import", "Provide the device ID as the import ID (for example sensor-01) or as the identity attribute `device_id`.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("device_id"), types.StringValue(deviceID))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), types.StringValue(deviceID))...)
	resp.Diagnostics.Append(setIdentity(ctx, resp.Identity, deviceID)...)
}

// ---- helpers ----------------------------------------------------------------

// writeOnlyKeys collects the write-only key arguments of the model.
func (m resourceModel) writeOnlyKeys() identity.WriteOnlyKeys {
	return identity.WriteOnlyKeys{Primary: m.PrimaryKeyWO, PrimaryVersion: m.PrimaryKeyWOVersion, Secondary: m.SecondaryKeyWO, SecondaryVersion: m.SecondaryKeyWOVersion}
}

// keysInState reports whether hub-generated/plain keys are stored in state
// (true) or write-only arguments are in use for the respective slot (false).
func (m resourceModel) keysInState() (primary, secondary bool) {
	return m.writeOnlyKeys().KeysInState()
}

// spec builds the full identity to write from plan + config (+ the current
// hub state on update, for keys not managed by Terraform).
func (r *deviceResource) spec(ctx context.Context, plan, config resourceModel, current *client.Device) (client.DeviceSpec, diag.Diagnostics) {
	spec := client.DeviceSpec{
		DeviceID:     plan.DeviceID.ValueString(),
		Status:       plan.Status.ValueString(),
		StatusReason: plan.StatusReason.ValueString(),
		IotEdge:      plan.EdgeEnabled.ValueBool(),
		ParentScope:  plan.ParentScope.ValueString(),
	}
	var currentAuth *client.AuthenticationMechanism
	if current != nil {
		currentAuth = current.Authentication
	}
	auth, diags := identity.BuildAuth(ctx, plan.Authentication, config.writeOnlyKeys(), currentAuth)
	spec.Authentication = auth
	return spec, diags
}

// writtenFromState reflects the prior state for conflict inspection.
func writtenFromState(state resourceModel, auth identity.Auth) writtenFields {
	return writtenFields{
		Status:       state.Status.ValueString(),
		StatusReason: state.StatusReason.ValueString(),
		IotEdge:      state.EdgeEnabled.ValueBool(),
		ParentScope:  state.ParentScope.ValueString(),
		Auth:         identity.WrittenAuthFromState(auth),
	}
}

// setState maps a hub identity onto the model.
func setState(m *resourceModel, d *client.Device, primaryInState, secondaryInState bool) {
	m.ID = types.StringValue(d.DeviceID)
	m.DeviceID = types.StringValue(d.DeviceID)
	m.Status = types.StringValue(d.Status)
	m.StatusReason = types.StringNull()
	if d.StatusReason != nil && *d.StatusReason != "" {
		m.StatusReason = types.StringValue(*d.StatusReason)
	}
	m.EdgeEnabled = types.BoolValue(d.Capabilities != nil && d.Capabilities.IotEdge)
	m.ParentScope = identity.StringOrNull(parentScopeOf(d))
	auth := identity.AuthFromHub(d.Authentication, true)
	if !primaryInState {
		auth.PrimaryKey = types.StringNull()
	}
	if !secondaryInState {
		auth.SecondaryKey = types.StringNull()
	}
	m.Authentication = auth.Object()
	m.ETag = types.StringValue(d.ETag)
	m.GenerationID = types.StringValue(d.GenerationID)
	m.DeviceScope = identity.StringOrNull(d.DeviceScope)
	m.ConnectionState = identity.StringOrNull(d.ConnectionState)
	m.ConnectionStateUpdatedTime = identity.TimeOrNull(d.ConnectionStateUpdatedTime)
	m.LastActivityTime = identity.TimeOrNull(d.LastActivityTime)
	m.StatusUpdatedTime = identity.TimeOrNull(d.StatusUpdatedTime)
	m.CloudToDeviceMessageCount = types.Int64Value(d.CloudToDeviceMessageCount)
}
