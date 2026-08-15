// Package module implements iothub_module (resource, data source), the
// iothub_modules data source and the iothub_module_credentials ephemeral
// resource. Behaviour mirrors iothub_device (CONCEPT.md §6.2): the same
// authentication block, the same create-only PUT / If-Match update / conflict
// inspection semantics — modules just have no status and no scopes.
package module

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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
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
	_ resource.Resource                   = &moduleResource{}
	_ resource.ResourceWithConfigure      = &moduleResource{}
	_ resource.ResourceWithImportState    = &moduleResource{}
	_ resource.ResourceWithModifyPlan     = &moduleResource{}
	_ resource.ResourceWithValidateConfig = &moduleResource{}
	_ resource.ResourceWithIdentity       = &moduleResource{}
)

// NewResource returns the iothub_module resource.
func NewResource() resource.Resource { return &moduleResource{} }

type moduleResource struct {
	pd *common.ProviderData
}

type resourceModel struct {
	ID                         types.String   `tfsdk:"id"`
	Hostname                   types.String   `tfsdk:"hostname"`
	DeviceID                   types.String   `tfsdk:"device_id"`
	ModuleID                   types.String   `tfsdk:"module_id"`
	ManagedBy                  types.String   `tfsdk:"managed_by"`
	Authentication             types.Object   `tfsdk:"authentication"`
	PrimaryKeyWO               types.String   `tfsdk:"primary_key_wo"`
	PrimaryKeyWOVersion        types.Int64    `tfsdk:"primary_key_wo_version"`
	SecondaryKeyWO             types.String   `tfsdk:"secondary_key_wo"`
	SecondaryKeyWOVersion      types.Int64    `tfsdk:"secondary_key_wo_version"`
	ETag                       types.String   `tfsdk:"etag"`
	GenerationID               types.String   `tfsdk:"generation_id"`
	ConnectionState            types.String   `tfsdk:"connection_state"`
	ConnectionStateUpdatedTime types.String   `tfsdk:"connection_state_updated_time"`
	LastActivityTime           types.String   `tfsdk:"last_activity_time"`
	CloudToDeviceMessageCount  types.Int64    `tfsdk:"cloud_to_device_message_count"`
	Timeouts                   timeouts.Value `tfsdk:"timeouts"`
}

const defaultTimeout = 20 * time.Minute

func (r *moduleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_module"
}

func (r *moduleResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	computed := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{MarkdownDescription: desc, Computed: true}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A module identity on a device. A module has its own credentials and its own twin. Manage the twin " +
			"with `iothub_module_twin`. Deleting a module deletes its twin. Modules have no status of their own: a disabled device " +
			"disables its modules.\n\n" +
			"Only `device_id`, `module_id` and `hostname` force replacement. Every other attribute changes in place.\n\n" +
			"Credentials work as for `iothub_device`. The hub generates SAS keys by default and they are stored in state as " +
			"sensitive values. Use the write-only `primary_key_wo` and `secondary_key_wo` arguments to keep keys out of state, " +
			"and `iothub_module_credentials` to read connection strings. IoT Edge devices get the system modules `$edgeAgent` " +
			"and `$edgeHub` from the hub. Those are not created through this resource.\n\n" +
			"~> With SAS authentication the hub refuses to create, change or delete modules of a *disabled* device. Enable the " +
			"device first, or authenticate with Entra ID. Refreshing an existing module still works.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "`<hostname>/devices/<device_id>/modules/<module_id>`. Also the import ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"hostname": schema.StringAttribute{
				MarkdownDescription: common.HostnameAttributeDescription + " Changing it replaces the module.",
				Optional:            true,
				Computed:            true,
				Validators:          common.HostnameValidators(),
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"device_id": schema.StringAttribute{
				MarkdownDescription: "ID of the device the module belongs to. Changing it replaces the module.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{identity.IDValidator()},
			},
			"module_id": schema.StringAttribute{
				MarkdownDescription: "Module ID: " + identity.IDDescription + ". Changing it replaces the module. IDs starting with `$` are reserved for the hub's system modules.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators: []validator.String{
					identity.IDValidator(),
					stringvalidator.RegexMatches(notSystemModulePattern, "must not start with $ (reserved for system modules such as $edgeAgent)"),
				},
			},
			"managed_by": schema.StringAttribute{
				MarkdownDescription: "Free-text owner of the module. The hub sets `iotEdge` on its system modules.",
				Optional:            true,
			},
			"authentication": identity.AuthAttribute("module"),
			// ---- computed ----
			"etag": computed("ETag of the module identity."),
			"generation_id": schema.StringAttribute{
				MarkdownDescription: "Hub-generated ID that changes when a module with the same ID is re-created.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"connection_state":              computed("`Connected` or `Disconnected`. Updated by the service and approximate."),
			"connection_state_updated_time": computed("When the connection state last changed."),
			"last_activity_time":            computed("Last time the module connected, sent or received a message."),
			"cloud_to_device_message_count": schema.Int64Attribute{
				MarkdownDescription: "Number of cloud-to-device messages queued for the module.",
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

// identityModel is the resource identity: hub, device and module ID.
type identityModel struct {
	Hostname types.String `tfsdk:"hostname"`
	DeviceID types.String `tfsdk:"device_id"`
	ModuleID types.String `tfsdk:"module_id"`
}

func (r *moduleResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	resp.IdentitySchema = identityschema.Schema{
		Attributes: map[string]identityschema.Attribute{
			"hostname":  identityschema.StringAttribute{RequiredForImport: true, Description: "IoT Hub hostname."},
			"device_id": identityschema.StringAttribute{RequiredForImport: true, Description: "Device ID."},
			"module_id": identityschema.StringAttribute{RequiredForImport: true, Description: "Module ID."},
		},
	}
}

func setIdentity(ctx context.Context, id *tfsdk.ResourceIdentity, hostname, deviceID, moduleID string) diag.Diagnostics {
	if id == nil {
		return nil
	}
	return id.Set(ctx, &identityModel{Hostname: types.StringValue(hostname), DeviceID: types.StringValue(deviceID), ModuleID: types.StringValue(moduleID)})
}

func (r *moduleResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	pd, diags := common.ExpectProviderData(req.ProviderData)
	resp.Diagnostics.Append(diags...)
	r.pd = pd
}

func (r *moduleResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var data resourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(identity.ValidateAuth(ctx, data.Authentication, data.writeOnlyKeys())...)
}

// ModifyPlan resolves the hostname, defers when the hub is not known yet, and
// keeps the planned authentication object consistent with its type.
func (r *moduleResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() { // destroy
		return
	}
	var plan, config resourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	stateAuth := types.ObjectNull(identity.AuthAttrTypes)
	if !req.State.Raw.IsNull() {
		var state resourceModel
		resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
		if resp.Diagnostics.HasError() {
			return
		}
		stateAuth = state.Authentication
	}

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
	if !plan.Hostname.IsUnknown() && !plan.DeviceID.IsUnknown() && !plan.ModuleID.IsUnknown() {
		plan.ID = types.StringValue(resourceID(plan.Hostname.ValueString(), plan.DeviceID.ValueString(), plan.ModuleID.ValueString()))
	}

	auth, diags := identity.PlanAuth(ctx, config.Authentication, stateAuth, config.writeOnlyKeys())
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.Authentication = auth
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *moduleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan, config resourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := plan.Timeouts.Create(ctx, defaultTimeout)
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

	tflog.Debug(ctx, "creating IoT Hub module", map[string]any{"hostname": hostname, "device_id": spec.DeviceID, "module_id": spec.ModuleID})
	created, err := c.CreateModule(ctx, spec)
	if err != nil {
		switch {
		case client.IsConflict(err):
			resp.Diagnostics.AddAttributeError(path.Root("module_id"), "Module already exists",
				fmt.Sprintf("Device %q in %s already has a module with ID %q. To manage it with Terraform, import it:\n\n  terraform import <address> %s\n\n%s",
					spec.DeviceID, hostname, spec.ModuleID, resourceID(hostname, spec.DeviceID, spec.ModuleID), common.DescribeError(err)))
		case client.IsNotFound(err):
			resp.Diagnostics.AddAttributeError(path.Root("device_id"), "Device not found",
				fmt.Sprintf("No device with ID %q exists in %s; create it first (e.g. with `iothub_device`).\n\n%s", spec.DeviceID, hostname, common.DescribeError(err)))
		default:
			resp.Diagnostics.AddError("Cannot create IoT Hub module", common.DescribeError(err))
		}
		return
	}
	setState(&plan, hostname, created, plan.writeOnlyKeys())
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	resp.Diagnostics.Append(setIdentity(ctx, resp.Identity, hostname, created.DeviceID, created.ModuleID)...)
}

func (r *moduleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state resourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := state.Timeouts.Read(ctx, defaultTimeout)
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
	// ETag-gated refresh (CONCEPT.md §11.2): the module twin's deviceEtag is
	// the module identity's ETag; when it matches, only the volatile fields
	// need refreshing and the registry read is skipped.
	if tw := r.pd.Refresh.TwinIfUnchanged(ctx, hostname, func(ctx context.Context) (*client.Twin, error) {
		return c.GetModuleTwin(ctx, state.DeviceID.ValueString(), state.ModuleID.ValueString())
	}, state.ETag.ValueString(), state.ConnectionState.ValueString()); tw != nil {
		state.LastActivityTime = identity.TimeOrNull(tw.LastActivityTime)
		state.CloudToDeviceMessageCount = types.Int64Value(tw.CloudToDeviceMessageCount)
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		resp.Diagnostics.Append(setIdentity(ctx, resp.Identity, hostname, state.DeviceID.ValueString(), state.ModuleID.ValueString())...)
		return
	}
	mod, err := c.GetModule(ctx, state.DeviceID.ValueString(), state.ModuleID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			tflog.Info(ctx, "IoT Hub module no longer exists; removing from state", map[string]any{"device_id": state.DeviceID.ValueString(), "module_id": state.ModuleID.ValueString()})
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Cannot read IoT Hub module", common.DescribeError(err))
		return
	}
	setState(&state, hostname, mod, state.writeOnlyKeys())
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	resp.Diagnostics.Append(setIdentity(ctx, resp.Identity, hostname, mod.DeviceID, mod.ModuleID)...)
}

func (r *moduleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, config, state resourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := plan.Timeouts.Update(ctx, defaultTimeout)
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
	deviceID, moduleID := state.DeviceID.ValueString(), state.ModuleID.ValueString()
	name := fmt.Sprintf("module %q of device %q", moduleID, deviceID)

	// Re-read right before the full-body PUT (current keys for write-only
	// slots, fresh ETag, conflict inspection) — CONCEPT.md §11.1/§11.3.
	var updated *client.Module
	for attempt := 1; ; attempt++ {
		fresh, err := c.GetModule(ctx, deviceID, moduleID)
		if err != nil {
			if client.IsNotFound(err) {
				resp.Diagnostics.AddError("Module no longer exists",
					fmt.Sprintf("The %s was deleted outside Terraform. Run `terraform plan` again; it will be re-created.", name))
				return
			}
			resp.Diagnostics.AddError("Cannot read IoT Hub module before update", common.DescribeError(err))
			return
		}
		if fresh.ETag != state.ETag.ValueString() {
			priorAuth, _, d := identity.AuthFromObject(ctx, state.Authentication)
			resp.Diagnostics.Append(d...)
			if changed := diffWritten(writtenFromState(state, priorAuth), writtenFromHub(fresh)); len(changed) > 0 {
				resp.Diagnostics.AddError("Module changed outside Terraform",
					fmt.Sprintf("The %s was modified since the last refresh:\n  - %s\n\nRun `terraform plan` again to review the external change before applying.", name, strings.Join(changed, "\n  - ")))
				return
			}
			tflog.Debug(ctx, "module ETag moved without a change to written fields; continuing", map[string]any{"module_id": moduleID})
		}
		spec, d := r.spec(ctx, plan, config, fresh)
		resp.Diagnostics.Append(d...)
		if resp.Diagnostics.HasError() {
			return
		}
		updated, err = c.UpdateModule(ctx, spec, fresh.ETag)
		if err == nil {
			break
		}
		if client.IsPreconditionFailed(err) && attempt < 3 {
			tflog.Debug(ctx, "412 on update; re-reading and retrying", map[string]any{"module_id": moduleID, "attempt": attempt})
			continue
		}
		resp.Diagnostics.AddError("Cannot update IoT Hub module", common.DescribeError(err))
		return
	}
	setState(&plan, hostname, updated, plan.writeOnlyKeys())
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	resp.Diagnostics.Append(setIdentity(ctx, resp.Identity, hostname, updated.DeviceID, updated.ModuleID)...)
}

func (r *moduleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state resourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := state.Timeouts.Delete(ctx, defaultTimeout)
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
	// A 404 also covers the device having been deleted first (which removes
	// its modules).
	if err := c.DeleteModule(ctx, state.DeviceID.ValueString(), state.ModuleID.ValueString(), "*"); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Cannot delete IoT Hub module", common.DescribeError(err))
	}
}

func (r *moduleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	var hostname, deviceID, moduleID string
	if req.ID != "" {
		host, parts, err := common.ParseResourceID(req.ID, "devices")
		if err != nil || len(parts) != 3 || parts[1] != "modules" || parts[2] == "" {
			resp.Diagnostics.AddError("Invalid import ID", "Expected `<hostname>/devices/<device_id>/modules/<module_id>`, e.g. contoso.azure-devices.net/devices/sensor-01/modules/telemetry.")
			return
		}
		hostname, deviceID, moduleID = host, parts[0], parts[2]
	} else if req.Identity != nil {
		var id identityModel
		resp.Diagnostics.Append(req.Identity.Get(ctx, &id)...)
		if resp.Diagnostics.HasError() {
			return
		}
		hostname, deviceID, moduleID = id.Hostname.ValueString(), id.DeviceID.ValueString(), id.ModuleID.ValueString()
	}
	if hostname == "" || deviceID == "" || moduleID == "" {
		resp.Diagnostics.AddError("Invalid import", "Provide the import ID `<hostname>/devices/<device_id>/modules/<module_id>` or the identity attributes `hostname`, `device_id` and `module_id`.")
		return
	}
	hostname = strings.ToLower(hostname)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("hostname"), types.StringValue(hostname))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("device_id"), types.StringValue(deviceID))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("module_id"), types.StringValue(moduleID))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), types.StringValue(resourceID(hostname, deviceID, moduleID)))...)
	resp.Diagnostics.Append(setIdentity(ctx, resp.Identity, hostname, deviceID, moduleID)...)
}

// ---- helpers ----------------------------------------------------------------

func resourceID(hostname, deviceID, moduleID string) string {
	return common.ResourceID(hostname, "devices", deviceID, "modules", moduleID)
}

func (m resourceModel) writeOnlyKeys() identity.WriteOnlyKeys {
	return identity.WriteOnlyKeys{Primary: m.PrimaryKeyWO, PrimaryVersion: m.PrimaryKeyWOVersion, Secondary: m.SecondaryKeyWO, SecondaryVersion: m.SecondaryKeyWOVersion}
}

func (r *moduleResource) client(ctx context.Context, hostname types.String) (*client.Client, string, diag.Diagnostics) {
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

// spec builds the full module identity to write.
func (r *moduleResource) spec(ctx context.Context, plan, config resourceModel, current *client.Module) (client.ModuleSpec, diag.Diagnostics) {
	spec := client.ModuleSpec{
		DeviceID:  plan.DeviceID.ValueString(),
		ModuleID:  plan.ModuleID.ValueString(),
		ManagedBy: plan.ManagedBy.ValueString(),
	}
	var currentAuth *client.AuthenticationMechanism
	if current != nil {
		currentAuth = current.Authentication
	}
	auth, diags := identity.BuildAuth(ctx, plan.Authentication, config.writeOnlyKeys(), currentAuth)
	spec.Authentication = auth
	return spec, diags
}

// writtenFields are the module fields the provider writes (conflict
// inspection, CONCEPT.md §11.1).
type writtenFields struct {
	ManagedBy string
	Auth      identity.WrittenAuth
}

func writtenFromHub(m *client.Module) writtenFields {
	return writtenFields{ManagedBy: m.ManagedBy, Auth: identity.WrittenAuthFromHub(m.Authentication)}
}

func writtenFromState(state resourceModel, auth identity.Auth) writtenFields {
	return writtenFields{ManagedBy: state.ManagedBy.ValueString(), Auth: identity.WrittenAuthFromState(auth)}
}

func diffWritten(prior, fresh writtenFields) []string {
	out := identity.DiffString(nil, "managed_by", prior.ManagedBy, fresh.ManagedBy)
	return append(out, identity.DiffAuth(prior.Auth, fresh.Auth)...)
}

// setState maps a hub module onto the model.
func setState(m *resourceModel, hostname string, mod *client.Module, wo identity.WriteOnlyKeys) {
	m.ID = types.StringValue(resourceID(hostname, mod.DeviceID, mod.ModuleID))
	m.Hostname = types.StringValue(hostname)
	m.DeviceID = types.StringValue(mod.DeviceID)
	m.ModuleID = types.StringValue(mod.ModuleID)
	m.ManagedBy = identity.StringOrNull(mod.ManagedBy)
	primaryInState, secondaryInState := wo.KeysInState()
	auth := identity.AuthFromHub(mod.Authentication, true)
	if !primaryInState {
		auth.PrimaryKey = types.StringNull()
	}
	if !secondaryInState {
		auth.SecondaryKey = types.StringNull()
	}
	m.Authentication = auth.Object()
	m.ETag = types.StringValue(mod.ETag)
	m.GenerationID = types.StringValue(mod.GenerationID)
	m.ConnectionState = identity.StringOrNull(mod.ConnectionState)
	m.ConnectionStateUpdatedTime = identity.TimeOrNull(mod.ConnectionStateUpdatedTime)
	m.LastActivityTime = identity.TimeOrNull(mod.LastActivityTime)
	m.CloudToDeviceMessageCount = types.Int64Value(mod.CloudToDeviceMessageCount)
}
