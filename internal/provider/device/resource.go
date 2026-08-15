package device

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/identityschema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
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
	Hostname                   types.String   `tfsdk:"hostname"`
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
	ParentScopes               types.List     `tfsdk:"parent_scopes"`
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
		MarkdownDescription: "A device identity in the IoT Hub identity registry (`PUT/GET/DELETE /devices/{id}`).\n\n" +
			"Creating a device implicitly creates its twin; manage tags and desired properties with " +
			"`iothub_device_twin`. Deleting a device deletes its twin and modules.\n\n" +
			"**Credentials.** With `authentication.type = \"sas\"` and no keys given, the hub generates the keys and " +
			"they are stored in state (sensitive). To keep keys out of state, pass them through the write-only " +
			"`primary_key_wo` / `secondary_key_wo` arguments (bump the matching `*_wo_version` to rotate) and read " +
			"connection strings with the `iothub_device_credentials` ephemeral resource.\n\n" +
			"**Concurrency.** Updates send `If-Match` with the ETag from the last refresh; if the identity changed " +
			"outside Terraform in the meantime the apply fails and names the changed fields — run `terraform plan` again.\n\n" +
			"**Refresh cost.** A refresh reads the device *twin* first (100 reads/s on every tier) and only falls back to the " +
			"identity registry (1.67 reads/s on S1) when the twin's `deviceEtag` shows the identity changed or the connection state " +
			"moved — the twin's `deviceEtag` is the identity ETag, so this is lossless. It needs the `twins/read` data action " +
			"(*IoT Hub Twin Contributor* / *Data Contributor*; *Registry Contributor* alone lacks it) or a SAS policy with " +
			"*ServiceConnect*; without it the registry is read as before, silently.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "`<hostname>/devices/<device_id>` — also the import ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"hostname": schema.StringAttribute{
				MarkdownDescription: common.HostnameAttributeDescription + " Changing it replaces the device.",
				Optional:            true,
				Computed:            true,
				Validators:          common.HostnameValidators(),
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"device_id": schema.StringAttribute{
				MarkdownDescription: "Device ID: " + identity.IDDescription + ". Immutable.",
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
				MarkdownDescription: "Whether the device is an IoT Edge device (`capabilities.iotEdge`). Edge devices get a hub-generated " +
					"`device_scope` and the `$edgeAgent`/`$edgeHub` module identities. Can be changed in place.",
				Optional:      true,
				Computed:      true,
				Default:       booldefault.StaticBool(false),
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"parent_scope": schema.StringAttribute{
				MarkdownDescription: "`device_scope` of the parent IoT Edge device, making this device its child " +
					"(a downstream/leaf device, or a nested edge device). One parent per device.",
				Optional:   true,
				Validators: []validator.String{stringvalidator.RegexMatches(regexpPrefix(parentScopePrefix), "must be an edge device scope starting with "+parentScopePrefix)},
			},
			"authentication": identity.AuthAttribute("device"),
			// ---- computed ----
			"etag":                          computedString("Identity ETag; used for optimistic concurrency on updates."),
			"generation_id":                 computedStringStable("Hub-generated ID distinguishing re-creations of the same `device_id`."),
			"device_scope":                  computedString("Own scope: hub-generated for edge devices, the parent's scope for child leaf devices, otherwise empty."),
			"connection_state":              computedString("`Connected` or `Disconnected` (approximate, updated by the service)."),
			"connection_state_updated_time": computedString("When the connection state last changed (re-read from the registry when `connection_state` changed)."),
			"last_activity_time":            computedString("Last time the device connected, sent or received a message."),
			"status_updated_time":           computedString("When `status` last changed."),
			"cloud_to_device_message_count": schema.Int64Attribute{
				MarkdownDescription: "Number of cloud-to-device messages queued for the device.",
				Computed:            true,
			},
			"parent_scopes": schema.ListAttribute{
				MarkdownDescription: "Scopes of the parent edge device(s) as reported by the hub.",
				ElementType:         types.StringType,
				Computed:            true,
			},
		},
		Blocks: map[string]schema.Block{
			"timeouts": timeouts.Block(ctx, timeouts.Opts{Create: true, Read: true, Update: true, Delete: true}),
		},
	}
	for name, a := range identity.WriteOnlyKeyAttributes() {
		resp.Schema.Attributes[name] = a
	}
}

// identityModel is the resource identity: the hub and the device ID.
type identityModel struct {
	Hostname types.String `tfsdk:"hostname"`
	DeviceID types.String `tfsdk:"device_id"`
}

func (r *deviceResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	resp.IdentitySchema = identityschema.Schema{
		Attributes: map[string]identityschema.Attribute{
			"hostname":  identityschema.StringAttribute{RequiredForImport: true, Description: "IoT Hub hostname."},
			"device_id": identityschema.StringAttribute{RequiredForImport: true, Description: "Device ID."},
		},
	}
}

func setIdentity(ctx context.Context, id *tfsdk.ResourceIdentity, hostname, deviceID string) diag.Diagnostics {
	if id == nil {
		return nil
	}
	return id.Set(ctx, &identityModel{Hostname: types.StringValue(hostname), DeviceID: types.StringValue(deviceID)})
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

// ModifyPlan resolves the hostname, defers when the hub is not known yet, and
// keeps the planned authentication object consistent with its type so the
// apply never produces a "inconsistent result" (CONCEPT.md §6.1).
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

	// hostname: config > state > provider default; unknown -> defer if allowed.
	if plan.Hostname.IsUnknown() && r.pd != nil {
		hostname, ok, diags := common.ResolveHostname(config.Hostname, r.pd)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		if ok {
			plan.Hostname = types.StringValue(strings.ToLower(hostname))
		} else if req.ClientCapabilities.DeferralAllowed {
			resp.Deferred = &resource.Deferred{Reason: resource.DeferredReasonProviderConfigUnknown}
		}
	}
	if !plan.Hostname.IsUnknown() && !plan.DeviceID.IsUnknown() {
		plan.ID = types.StringValue(common.ResourceID(plan.Hostname.ValueString(), "devices", plan.DeviceID.ValueString()))
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

	c, hostname, diags := r.client(ctx, plan.Hostname)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

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
					spec.DeviceID, hostname, common.ResourceID(hostname, "devices", spec.DeviceID), common.DescribeError(err)))
			return
		}
		resp.Diagnostics.AddError("Cannot create IoT Hub device", common.DescribeError(err))
		return
	}
	pk, sk := plan.keysInState()
	resp.Diagnostics.Append(r.setState(ctx, &plan, hostname, created, pk, sk)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	resp.Diagnostics.Append(setIdentity(ctx, resp.Identity, hostname, created.DeviceID)...)
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

	c, hostname, diags := r.client(ctx, state.Hostname)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	// ETag-gated refresh (CONCEPT.md §11.2): the twin is cheap to read and its
	// deviceEtag is the identity ETag; when it matches, only the volatile
	// fields need refreshing and the registry read is skipped.
	if tw := r.pd.Refresh.TwinIfUnchanged(ctx, hostname, func(ctx context.Context) (*client.Twin, error) {
		return c.GetDeviceTwin(ctx, state.DeviceID.ValueString())
	}, state.ETag.ValueString(), state.ConnectionState.ValueString()); tw != nil {
		state.LastActivityTime = identity.TimeOrNull(tw.LastActivityTime)
		state.CloudToDeviceMessageCount = types.Int64Value(tw.CloudToDeviceMessageCount)
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		resp.Diagnostics.Append(setIdentity(ctx, resp.Identity, hostname, state.DeviceID.ValueString())...)
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
	resp.Diagnostics.Append(r.setState(ctx, &state, hostname, dev, pk, sk)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	resp.Diagnostics.Append(setIdentity(ctx, resp.Identity, hostname, dev.DeviceID)...)
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

	c, hostname, diags := r.client(ctx, plan.Hostname)
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
	resp.Diagnostics.Append(r.setState(ctx, &plan, hostname, updated, pk, sk)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	resp.Diagnostics.Append(setIdentity(ctx, resp.Identity, hostname, updated.DeviceID)...)
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

	c, _, diags := r.client(ctx, state.Hostname)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := c.DeleteDevice(ctx, state.DeviceID.ValueString(), "*"); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Cannot delete IoT Hub device", common.DescribeError(err))
	}
}

func (r *deviceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	var hostname, deviceID string
	if req.ID != "" {
		host, parts, err := common.ParseResourceID(req.ID, "devices")
		if err != nil || len(parts) != 1 {
			resp.Diagnostics.AddError("Invalid import ID", "Expected `<hostname>/devices/<device_id>`, e.g. contoso.azure-devices.net/devices/sensor-01.")
			return
		}
		hostname, deviceID = host, parts[0]
	} else if req.Identity != nil {
		var id identityModel
		resp.Diagnostics.Append(req.Identity.Get(ctx, &id)...)
		if resp.Diagnostics.HasError() {
			return
		}
		hostname, deviceID = id.Hostname.ValueString(), id.DeviceID.ValueString()
	}
	if hostname == "" || deviceID == "" {
		resp.Diagnostics.AddError("Invalid import", "Provide the import ID `<hostname>/devices/<device_id>` or the identity attributes `hostname` and `device_id`.")
		return
	}
	hostname = strings.ToLower(hostname)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("hostname"), types.StringValue(hostname))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("device_id"), types.StringValue(deviceID))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), types.StringValue(common.ResourceID(hostname, "devices", deviceID)))...)
	resp.Diagnostics.Append(setIdentity(ctx, resp.Identity, hostname, deviceID)...)
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

func (r *deviceResource) client(ctx context.Context, hostname types.String) (*client.Client, string, diag.Diagnostics) {
	var diags diag.Diagnostics
	if hostname.IsUnknown() || hostname.IsNull() || hostname.ValueString() == "" {
		diags.AddAttributeError(path.Root("hostname"), "IoT Hub hostname unknown at apply time",
			"Set `hostname` on the resource or on the provider block.")
		return nil, "", diags
	}
	c, d := r.pd.ClientFor(ctx, hostname.ValueString())
	diags.Append(d...)
	if diags.HasError() {
		return nil, "", diags
	}
	return c, c.Hostname(), nil
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
func (r *deviceResource) setState(_ context.Context, m *resourceModel, hostname string, d *client.Device, primaryInState, secondaryInState bool) diag.Diagnostics {
	m.ID = types.StringValue(common.ResourceID(hostname, "devices", d.DeviceID))
	m.Hostname = types.StringValue(hostname)
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
	scopes := make([]attr.Value, 0, len(d.ParentScopes))
	for _, s := range d.ParentScopes {
		scopes = append(scopes, types.StringValue(s))
	}
	list, diags := types.ListValue(types.StringType, scopes)
	m.ParentScopes = list
	m.ConnectionState = identity.StringOrNull(d.ConnectionState)
	m.ConnectionStateUpdatedTime = identity.TimeOrNull(d.ConnectionStateUpdatedTime)
	m.LastActivityTime = identity.TimeOrNull(d.LastActivityTime)
	m.StatusUpdatedTime = identity.TimeOrNull(d.StatusUpdatedTime)
	m.CloudToDeviceMessageCount = types.Int64Value(d.CloudToDeviceMessageCount)
	return diags
}
