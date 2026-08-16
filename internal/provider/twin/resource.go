package twin

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/common"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/identity"
	"github.com/mwalser/terraform-provider-iothub/internal/twinpatch"
)

var (
	_ resource.Resource                = &twinResource{}
	_ resource.ResourceWithConfigure   = &twinResource{}
	_ resource.ResourceWithImportState = &twinResource{}
	_ resource.ResourceWithModifyPlan  = &twinResource{}
)

// kind distinguishes device twins from module twins; everything else is
// shared.
type kind int

const (
	deviceKind kind = iota
	moduleKind
)

func (k kind) isModule() bool { return k == moduleKind }

func (k kind) noun() string {
	if k.isModule() {
		return "module twin"
	}
	return "device twin"
}

// NewDeviceResource returns the iothub_device_twin resource.
func NewDeviceResource() resource.Resource { return &twinResource{kind: deviceKind} }

// NewModuleResource returns the iothub_module_twin resource.
func NewModuleResource() resource.Resource { return &twinResource{kind: moduleKind} }

type twinResource struct {
	kind kind
	pd   *common.ProviderData
}

// resourceModel is shared by both kinds; ModuleID is absent from the device
// twin schema and stays null there.
type resourceModel struct {
	ID                types.String `tfsdk:"id"`
	DeviceID          types.String `tfsdk:"device_id"`
	ModuleID          types.String `tfsdk:"module_id"`
	Tags              Document     `tfsdk:"tags"`
	DesiredProperties Document     `tfsdk:"desired_properties"`
	ETag              types.String `tfsdk:"etag"`
	Version           types.Int64  `tfsdk:"version"`
}

type deviceModel struct {
	ID                types.String `tfsdk:"id"`
	DeviceID          types.String `tfsdk:"device_id"`
	Tags              Document     `tfsdk:"tags"`
	DesiredProperties Document     `tfsdk:"desired_properties"`
	ETag              types.String `tfsdk:"etag"`
	Version           types.Int64  `tfsdk:"version"`
}

func (r *twinResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	if r.kind.isModule() {
		resp.TypeName = req.ProviderTypeName + "_module_twin"
	} else {
		resp.TypeName = req.ProviderTypeName + "_device_twin"
	}
}

func (r *twinResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	subject, idFormat := "device", "The device ID"
	if r.kind.isModule() {
		subject, idFormat = "module", "`<device_id>/<module_id>`"
	}
	attrs := map[string]schema.Attribute{
		"id": schema.StringAttribute{
			MarkdownDescription: idFormat + ". Also the import ID. Every twin resource that manages the same twin has the same `id`.",
			Computed:            true,
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
		"device_id": schema.StringAttribute{
			MarkdownDescription: "ID of the device" + map[bool]string{true: " the module belongs to", false: ""}[r.kind.isModule()] + ". Changing it replaces the resource.",
			Required:            true,
			PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			Validators:          []validator.String{identity.IDValidator()},
		},
		"tags": schema.StringAttribute{
			CustomType:          DocumentType,
			MarkdownDescription: "The tags to manage, as a JSON object (`jsonencode`).",
			Optional:            true,
		},
		"desired_properties": schema.StringAttribute{
			CustomType:          DocumentType,
			MarkdownDescription: "The desired properties to manage, as a JSON object (`jsonencode`).",
			Optional:            true,
		},
		"etag":    schema.StringAttribute{MarkdownDescription: "ETag of the twin.", Computed: true},
		"version": schema.Int64Attribute{MarkdownDescription: "Version of the twin.", Computed: true},
	}
	if r.kind.isModule() {
		attrs["module_id"] = schema.StringAttribute{
			MarkdownDescription: "Module ID. Changing it replaces the resource.",
			Required:            true,
			PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			Validators:          []validator.String{identity.IDValidator()},
		}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages part of a " + subject + " twin: the tags and desired properties you declare. Reported " +
			"properties are read-only, in the `iothub_" + subject + "_twin` data source.\n\n" +
			"## Ownership\n\n" +
			"The twin exists as long as the " + subject + " does. This resource neither creates nor deletes it. Terraform " +
			"manages only the keys you declare in `tags` and `desired_properties`, down to the innermost key, and leaves " +
			"everything else in the twin alone: a backend can write `desired.firmware.lastCheck` beside your " +
			"`desired.firmware.channel`. Arrays and empty objects count as one value, and `null` is not a value: omit a key to " +
			"remove it. Several resources and systems can share one twin as long as they declare different " +
			"keys. Two writers of the same key, including `iothub_configuration` and `iothub_scheduled_job` twin patches, " +
			"overwrite each other on every apply.\n\n" +
			"Removing a key from the configuration removes it from the twin, and destroying the resource removes every " +
			"managed key. An imported twin starts without managed keys. The first apply adopts the declared ones and deletes " +
			"nothing.\n\n" +
			"Keys and values must follow the [twin format rules](https://learn.microsoft.com/azure/iot-hub/iot-hub-devguide-device-twins#tags-and-properties-format). " +
			"Values are compared by content: key order, whitespace and `2` versus `2.0` are equal and never show as drift.",
		Attributes: attrs,
	}
}

func (r *twinResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	pd, diags := common.ExpectProviderData(req.ProviderData)
	resp.Diagnostics.Append(diags...)
	r.pd = pd
}

// ---- model plumbing (device schema has no module_id) -----------------------

type getter interface {
	Get(context.Context, any) diag.Diagnostics
}

type setter interface {
	Set(context.Context, any) diag.Diagnostics
}

func (r *twinResource) get(ctx context.Context, src getter) (resourceModel, diag.Diagnostics) {
	if r.kind.isModule() {
		var m resourceModel
		diags := src.Get(ctx, &m)
		return m, diags
	}
	var d deviceModel
	diags := src.Get(ctx, &d)
	return resourceModel{ID: d.ID, DeviceID: d.DeviceID, ModuleID: types.StringNull(), Tags: d.Tags,
		DesiredProperties: d.DesiredProperties, ETag: d.ETag, Version: d.Version}, diags
}

func (r *twinResource) set(ctx context.Context, dst setter, m resourceModel) diag.Diagnostics {
	if r.kind.isModule() {
		return dst.Set(ctx, &m)
	}
	return dst.Set(ctx, &deviceModel{ID: m.ID, DeviceID: m.DeviceID, Tags: m.Tags,
		DesiredProperties: m.DesiredProperties, ETag: m.ETag, Version: m.Version})
}

func (r *twinResource) resourceID(deviceID, moduleID string) string {
	if r.kind.isModule() {
		return common.ModuleID(deviceID, moduleID)
	}
	return deviceID
}

func (r *twinResource) name(m resourceModel) string {
	if r.kind.isModule() {
		return fmt.Sprintf("twin of module %q on device %q", m.ModuleID.ValueString(), m.DeviceID.ValueString())
	}
	return fmt.Sprintf("twin of device %q", m.DeviceID.ValueString())
}

func (r *twinResource) getTwin(ctx context.Context, c *client.Client, m resourceModel) (*client.Twin, error) {
	if r.kind.isModule() {
		return c.GetModuleTwin(ctx, m.DeviceID.ValueString(), m.ModuleID.ValueString())
	}
	return c.GetDeviceTwin(ctx, m.DeviceID.ValueString())
}

func (r *twinResource) patchTwin(ctx context.Context, c *client.Client, m resourceModel, patch client.TwinPatch) (*client.Twin, error) {
	if r.kind.isModule() {
		return c.PatchModuleTwin(ctx, m.DeviceID.ValueString(), m.ModuleID.ValueString(), patch)
	}
	return c.PatchDeviceTwin(ctx, m.DeviceID.ValueString(), patch)
}

// ---- plan ------------------------------------------------------------------

func (r *twinResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() { // destroy
		return
	}
	plan, diags := r.get(ctx, req.Plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	common.DeferIfHubUnknown(r.pd, req, resp)
	if !plan.DeviceID.IsUnknown() && !plan.ModuleID.IsUnknown() {
		plan.ID = types.StringValue(r.resourceID(plan.DeviceID.ValueString(), plan.ModuleID.ValueString()))
	}
	resp.Diagnostics.Append(r.set(ctx, &resp.Plan, plan)...)
}

// ---- CRUD ------------------------------------------------------------------

func (r *twinResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	plan, diags := r.get(ctx, req.Plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Nothing owned yet: the prior sections are empty, so every configured
	// leaf is set (unless the twin already holds it — adoption).
	prior := resourceModel{Tags: NewDocumentNull(), DesiredProperties: NewDocumentNull()}
	resp.Diagnostics.Append(r.write(ctx, &plan, prior, "create")...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(r.set(ctx, &resp.State, plan)...)
}

func (r *twinResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	state, diags := r.get(ctx, req.State)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, ok, diags := r.pd.Hub()
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() || !ok {
		// !ok: the hub is not known yet (first plan of a configuration that
		// also creates it); the prior state stands until apply.
		return
	}
	tw, err := r.getTwin(ctx, c, state)
	if err != nil {
		if client.IsNotFound(err) {
			tflog.Info(ctx, "twin no longer exists (identity deleted); removing from state", map[string]any{"id": state.ID.ValueString()})
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Cannot read IoT Hub "+r.kind.noun(), common.DescribeError(err))
		return
	}
	resp.Diagnostics.Append(r.project(&state, tw, state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(r.set(ctx, &resp.State, state)...)
}

func (r *twinResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	plan, diags := r.get(ctx, req.Plan)
	resp.Diagnostics.Append(diags...)
	state, d := r.get(ctx, req.State)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(r.write(ctx, &plan, state, "update")...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(r.set(ctx, &resp.State, plan)...)
}

func (r *twinResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	state, diags := r.get(ctx, req.State)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Destroy = remove every owned leaf. A twin whose identity is already
	// gone needs nothing (write treats that as done for op "delete").
	next := state
	next.Tags, next.DesiredProperties = NewDocumentNull(), NewDocumentNull()
	resp.Diagnostics.Append(r.write(ctx, &next, state, "delete")...)
}

func (r *twinResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	var deviceID, moduleID string
	var err error
	if r.kind.isModule() {
		deviceID, moduleID, err = common.ParseModuleID(req.ID)
	} else if deviceID = strings.TrimSpace(req.ID); deviceID == "" || strings.Contains(deviceID, "/") {
		err = fmt.Errorf("invalid device ID")
	}
	if err != nil {
		want := "the device ID, e.g. sensor-01"
		if r.kind.isModule() {
			want = "`<device_id>/<module_id>`, e.g. sensor-01/telemetry"
		}
		resp.Diagnostics.AddError("Invalid import ID", "Expected "+want+".")
		return
	}
	// The owned set starts empty: tags / desired_properties stay null and the
	// first apply adopts what the configuration declares.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("device_id"), types.StringValue(deviceID))...)
	if r.kind.isModule() {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("module_id"), types.StringValue(moduleID))...)
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), types.StringValue(r.resourceID(deviceID, moduleID)))...)
}

// ---- engine glue -------------------------------------------------------------

// write moves the owned leaves from prior to plan: it reads the twin, builds
// the merge patch per section, sends it (if anything is left to send),
// verifies that the twin now holds the planned leaves and fills the computed
// attributes of plan.
func (r *twinResource) write(ctx context.Context, plan *resourceModel, prior resourceModel, op string) diag.Diagnostics {
	var diags diag.Diagnostics
	c, d := r.pd.HubOrError()
	diags.Append(d...)
	if diags.HasError() {
		return diags
	}
	hostname := c.Hostname()
	remote, err := r.getTwin(ctx, c, *plan)
	if err != nil {
		if client.IsNotFound(err) {
			switch op {
			case "create":
				diags.AddAttributeError(path.Root("device_id"), r.kind.noun()+" not found",
					fmt.Sprintf("The %s does not exist in %s: the identity must exist before its twin can be managed (create it with iothub_device / iothub_module, or check the IDs).", r.name(*plan), hostname))
			case "delete":
				// the identity is gone, and its twin with it: nothing to remove
			default:
				diags.AddError(r.kind.noun()+" no longer exists",
					fmt.Sprintf("The %s is gone (the identity was deleted). Run `terraform plan` again.", r.name(*plan)))
			}
			return diags
		}
		diags.AddError("Cannot read IoT Hub "+r.kind.noun(), common.DescribeError(err))
		return diags
	}

	tagsRemote, d := decodeSection(remote.Tags, "tags")
	diags.Append(d...)
	desiredRemote, d := decodeSection(remote.Properties.Desired, "properties.desired")
	diags.Append(d...)
	if diags.HasError() {
		return diags
	}
	desiredRemote = twinpatch.StripSystem(desiredRemote)

	patch := client.TwinPatch{}
	patch.Tags, d = sectionPatch(prior.Tags, plan.Tags, tagsRemote, path.Root("tags"))
	diags.Append(d...)
	patch.Desired, d = sectionPatch(prior.DesiredProperties, plan.DesiredProperties, desiredRemote, path.Root("desired_properties"))
	diags.Append(d...)
	if diags.HasError() {
		return diags
	}

	result := remote
	if !patch.IsEmpty() {
		tflog.Debug(ctx, "patching twin", map[string]any{"id": plan.ID.ValueString(), "op": op,
			"tags": twinpatch.Encode(patch.Tags), "desired": twinpatch.Encode(patch.Desired)})
		result, err = r.patchTwin(ctx, c, *plan, patch)
		if err != nil {
			if client.IsNotFound(err) {
				if op != "delete" {
					diags.AddError(r.kind.noun()+" no longer exists", fmt.Sprintf("The %s disappeared while updating it. Run `terraform plan` again.", r.name(*plan)))
				}
				return diags
			}
			diags.AddError("Cannot update IoT Hub "+r.kind.noun(), common.DescribeError(err))
			return diags
		}
		tagsRemote, d = decodeSection(result.Tags, "tags")
		diags.Append(d...)
		desiredRemote, d = decodeSection(result.Properties.Desired, "properties.desired")
		diags.Append(d...)
		if diags.HasError() {
			return diags
		}
		desiredRemote = twinpatch.StripSystem(desiredRemote)
	}

	// Verify the write took: the twin must now hold every planned leaf with
	// the planned value. The plan's strings are then kept verbatim.
	diags.Append(verifySection(plan.Tags, tagsRemote, path.Root("tags"))...)
	diags.Append(verifySection(plan.DesiredProperties, desiredRemote, path.Root("desired_properties"))...)
	if diags.HasError() {
		return diags
	}
	plan.ID = types.StringValue(r.resourceID(result.DeviceID, result.ModuleID))
	plan.ETag = types.StringValue(result.ETag)
	plan.Version = types.Int64Value(result.Version)
	return diags
}

// sectionPatch builds the merge patch for one section.
func sectionPatch(prior, planned Document, remote map[string]any, p path.Path) (map[string]any, diag.Diagnostics) {
	var diags diag.Diagnostics
	priorDoc, _ := prior.Object()
	planDoc, err := planned.Object()
	if err != nil {
		diags.AddAttributeError(p, "Invalid twin document", err.Error())
		return nil, diags
	}
	return twinpatch.Diff(priorDoc, planDoc, remote), diags
}

// verifySection checks that remote holds every leaf of planned.
func verifySection(planned Document, remote map[string]any, p path.Path) diag.Diagnostics {
	var diags diag.Diagnostics
	want, err := planned.Object()
	if err != nil || want == nil {
		return diags
	}
	owned := twinpatch.Leaves(want)
	got := twinpatch.Project(remote, owned)
	if twinpatch.Equal(got, want) {
		return diags
	}
	var wrong []string
	gotLeaves := twinpatch.Leaves(got)
	for _, key := range twinpatch.SortedPaths(owned) {
		if g, ok := gotLeaves[key]; !ok || !twinpatch.Equal(g.Value, owned[key].Value) {
			wrong = append(wrong, key)
		}
	}
	diags.AddAttributeError(p, "Twin write did not take effect",
		fmt.Sprintf("After the update the twin does not hold the configured value at: %s. The service may have transformed the value "+
			"(for example numbers outside its supported range). Twin as read back: %s", strings.Join(wrong, ", "), twinpatch.Encode(got)))
	return diags
}

// project fills m from a twin: computed attributes, and each section as the
// projection of the leaves owned (declared by owner) onto the twin. A null
// section stays null. When the projection is semantically equal to what
// owner declares, owner's string is kept verbatim (no cosmetic diffs).
func (r *twinResource) project(m *resourceModel, tw *client.Twin, owner resourceModel) diag.Diagnostics {
	var diags diag.Diagnostics
	m.ID = types.StringValue(r.resourceID(tw.DeviceID, tw.ModuleID))
	m.ETag = types.StringValue(tw.ETag)
	m.Version = types.Int64Value(tw.Version)

	tags, d := decodeSection(tw.Tags, "tags")
	diags.Append(d...)
	desired, d := decodeSection(tw.Properties.Desired, "properties.desired")
	diags.Append(d...)
	if diags.HasError() {
		return diags
	}
	m.Tags, d = projectSection(owner.Tags, tags, path.Root("tags"))
	diags.Append(d...)
	m.DesiredProperties, d = projectSection(owner.DesiredProperties, twinpatch.StripSystem(desired), path.Root("desired_properties"))
	diags.Append(d...)
	return diags
}

func projectSection(owned Document, remote map[string]any, p path.Path) (Document, diag.Diagnostics) {
	var diags diag.Diagnostics
	if owned.IsNull() || owned.IsUnknown() {
		return NewDocumentNull(), diags
	}
	doc, err := owned.Object()
	if err != nil {
		diags.AddAttributeError(p, "Invalid twin document", err.Error())
		return owned, diags
	}
	got := twinpatch.Project(remote, twinpatch.Leaves(doc))
	if twinpatch.Equal(got, doc) {
		return owned, diags
	}
	return NewDocumentValue(twinpatch.Encode(got)), diags
}

func decodeSection(raw []byte, what string) (map[string]any, diag.Diagnostics) {
	var diags diag.Diagnostics
	if len(raw) == 0 || string(raw) == "null" {
		return map[string]any{}, diags
	}
	doc, err := twinpatch.Decode(string(raw))
	if err != nil {
		diags.AddError("Cannot decode twin "+what, err.Error())
		return nil, diags
	}
	return doc, diags
}
