package device

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/common"
)

var (
	_ resource.Resource                   = &deviceResource{}
	_ resource.ResourceWithConfigure      = &deviceResource{}
	_ resource.ResourceWithImportState    = &deviceResource{}
	_ resource.ResourceWithModifyPlan     = &deviceResource{}
	_ resource.ResourceWithValidateConfig = &deviceResource{}
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
			"outside Terraform in the meantime the apply fails and names the changed fields — run `terraform plan` again.",
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
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"device_id": schema.StringAttribute{
				MarkdownDescription: "Device ID: 1–128 ASCII characters from `A-Z a-z 0-9 - : . + % _ # * ? ! ( ) , = @ ; $ '`. Immutable.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{stringvalidator.RegexMatches(deviceIDPattern, "must be 1–128 characters from A-Z a-z 0-9 - : . + % _ # * ? ! ( ) , = @ ; $ '")},
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
			"authentication": schema.SingleNestedAttribute{
				MarkdownDescription: "How the device authenticates. When omitted, the hub generates SAS keys and the block reflects " +
					"whatever the hub holds (imported devices keep their credentials).",
				Optional: true,
				Computed: true,
				Attributes: map[string]schema.Attribute{
					"type": schema.StringAttribute{
						MarkdownDescription: "`sas` (symmetric keys, default), `selfSigned` (X.509 thumbprints) or `certificateAuthority` (X.509 CA-signed).",
						Optional:            true,
						Computed:            true,
						Default:             stringdefault.StaticString(client.AuthTypeSAS),
						Validators:          []validator.String{stringvalidator.OneOf(client.AuthTypeSAS, client.AuthTypeSelfSigned, client.AuthTypeCertificateAuthority)},
					},
					"primary_key": schema.StringAttribute{
						MarkdownDescription: "Base64 primary key (16–64 bytes). Hub-generated when omitted. Not returned when `primary_key_wo` is used.",
						Optional:            true,
						Computed:            true,
						Sensitive:           true,
					},
					"secondary_key": schema.StringAttribute{
						MarkdownDescription: "Base64 secondary key (16–64 bytes). Hub-generated when omitted. Not returned when `secondary_key_wo` is used.",
						Optional:            true,
						Computed:            true,
						Sensitive:           true,
					},
					"primary_thumbprint": schema.StringAttribute{
						MarkdownDescription: "Primary X.509 thumbprint (40 or 64 hex characters, no separators) for `selfSigned`.",
						Optional:            true,
						Validators:          []validator.String{stringvalidator.RegexMatches(thumbprintPattern, "must be 40 or 64 hex characters without separators")},
					},
					"secondary_thumbprint": schema.StringAttribute{
						MarkdownDescription: "Secondary X.509 thumbprint for `selfSigned`.",
						Optional:            true,
						Validators:          []validator.String{stringvalidator.RegexMatches(thumbprintPattern, "must be 40 or 64 hex characters without separators")},
					},
				},
			},
			"primary_key_wo": schema.StringAttribute{
				MarkdownDescription: "Write-only primary key (base64, 16–64 bytes): sent to the hub, never stored in state or plan. " +
					"Requires `primary_key_wo_version`; a changed version re-sends the value.",
				Optional:  true,
				WriteOnly: true,
				Sensitive: true,
				Validators: []validator.String{
					stringvalidator.AlsoRequires(path.MatchRoot("primary_key_wo_version")),
					stringvalidator.ConflictsWith(path.MatchRoot("authentication").AtName("primary_key")),
				},
			},
			"primary_key_wo_version": schema.Int64Attribute{
				MarkdownDescription: "Version marker for `primary_key_wo`; change it to rotate the key.",
				Optional:            true,
				Validators:          []validator.Int64{int64validator.AlsoRequires(path.MatchRoot("primary_key_wo"))},
			},
			"secondary_key_wo": schema.StringAttribute{
				MarkdownDescription: "Write-only secondary key; see `primary_key_wo`.",
				Optional:            true,
				WriteOnly:           true,
				Sensitive:           true,
				Validators: []validator.String{
					stringvalidator.AlsoRequires(path.MatchRoot("secondary_key_wo_version")),
					stringvalidator.ConflictsWith(path.MatchRoot("authentication").AtName("secondary_key")),
				},
			},
			"secondary_key_wo_version": schema.Int64Attribute{
				MarkdownDescription: "Version marker for `secondary_key_wo`; change it to rotate the key.",
				Optional:            true,
				Validators:          []validator.Int64{int64validator.AlsoRequires(path.MatchRoot("secondary_key_wo"))},
			},
			// ---- computed ----
			"etag":                          computedString("Identity ETag; used for optimistic concurrency on updates."),
			"generation_id":                 computedStringStable("Hub-generated ID distinguishing re-creations of the same `device_id`."),
			"device_scope":                  computedString("Own scope: hub-generated for edge devices, the parent's scope for child leaf devices, otherwise empty."),
			"connection_state":              computedString("`Connected` or `Disconnected` (approximate, updated by the service)."),
			"connection_state_updated_time": computedString("When the connection state last changed."),
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
// block: keys only with sas, thumbprints only with selfSigned (which needs at
// least the primary), nothing for certificateAuthority.
func (r *deviceResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var data resourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	auth, ok, diags := authFromObject(ctx, data.Authentication)
	resp.Diagnostics.Append(diags...)
	if !ok || auth.Type.IsUnknown() {
		return
	}
	authType := client.AuthTypeSAS
	if !auth.Type.IsNull() {
		authType = auth.Type.ValueString()
	}
	hasKeys := known(auth.PrimaryKey) || known(auth.SecondaryKey) || known(data.PrimaryKeyWO) || known(data.SecondaryKeyWO)
	hasThumbs := known(auth.PrimaryThumbprint) || known(auth.SecondaryThumbprint)
	p := path.Root("authentication")
	switch authType {
	case client.AuthTypeSAS:
		if hasThumbs {
			resp.Diagnostics.AddAttributeError(p.AtName("primary_thumbprint"), "Thumbprints need selfSigned authentication", "Set `authentication.type = \"selfSigned\"` or remove the thumbprints.")
		}
	case client.AuthTypeSelfSigned:
		if hasKeys {
			resp.Diagnostics.AddAttributeError(p.AtName("primary_key"), "Symmetric keys need sas authentication", "selfSigned devices authenticate with X.509 thumbprints; remove the keys.")
		}
		if auth.PrimaryThumbprint.IsNull() {
			resp.Diagnostics.AddAttributeError(p.AtName("primary_thumbprint"), "selfSigned authentication needs a thumbprint", "Set `authentication.primary_thumbprint` (the hub would accept a selfSigned device without one, but it could never connect).")
		}
	case client.AuthTypeCertificateAuthority:
		if hasKeys || hasThumbs {
			resp.Diagnostics.AddAttributeError(p.AtName("type"), "certificateAuthority takes no keys or thumbprints", "The device authenticates with a CA-signed certificate; remove keys and thumbprints.")
		}
	}
}

func known(s types.String) bool { return !s.IsNull() && !s.IsUnknown() }

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
	resp.Diagnostics.Append(r.planAuthentication(ctx, &plan, config, state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

// planAuthentication decides the planned `authentication` object:
//   - config omitted: keep what the hub holds (state), unknown on create;
//   - sas: thumbprints null; keys from config, else from state when the type
//     was sas before, else unknown (hub-generated or write-only);
//   - selfSigned / certificateAuthority: keys null.
//
// Keys managed through write-only arguments are always planned null.
func (r *deviceResource) planAuthentication(ctx context.Context, plan *resourceModel, config resourceModel, state *resourceModel) diag.Diagnostics {
	var diags diag.Diagnostics
	cfg, cfgSet, d := authFromObject(ctx, config.Authentication)
	diags.Append(d...)
	var st authModel
	stSet := false
	if state != nil {
		st, stSet, d = authFromObject(ctx, state.Authentication)
		diags.Append(d...)
	}
	if diags.HasError() {
		return diags
	}
	if !cfgSet {
		if stSet {
			plan.Authentication = st.object()
		} else {
			plan.Authentication = types.ObjectUnknown(authAttrTypes)
		}
		return diags
	}

	authType := client.AuthTypeSAS
	if known(cfg.Type) {
		authType = cfg.Type.ValueString()
	}
	out := authModel{
		Type:                types.StringValue(authType),
		PrimaryKey:          types.StringNull(),
		SecondaryKey:        types.StringNull(),
		PrimaryThumbprint:   types.StringNull(),
		SecondaryThumbprint: types.StringNull(),
	}
	switch authType {
	case client.AuthTypeSAS:
		pick := func(configured types.String, woVersion types.Int64, stateVal types.String) types.String {
			switch {
			case known(configured):
				return configured
			case !woVersion.IsNull(): // write-only in use: never in state
				return types.StringNull()
			case stSet && st.Type.ValueString() == client.AuthTypeSAS && known(stateVal):
				return stateVal
			default:
				return types.StringUnknown()
			}
		}
		out.PrimaryKey = pick(cfg.PrimaryKey, config.PrimaryKeyWOVersion, st.PrimaryKey)
		out.SecondaryKey = pick(cfg.SecondaryKey, config.SecondaryKeyWOVersion, st.SecondaryKey)
	case client.AuthTypeSelfSigned:
		out.PrimaryThumbprint = cfg.PrimaryThumbprint
		out.SecondaryThumbprint = cfg.SecondaryThumbprint
	}
	plan.Authentication = out.object()
	return diags
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
			priorAuth, _, d := authFromObject(ctx, state.Authentication)
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
	hostname, parts, err := common.ParseResourceID(req.ID, "devices")
	if err != nil || len(parts) != 1 {
		resp.Diagnostics.AddError("Invalid import ID", "Expected `<hostname>/devices/<device_id>`, e.g. contoso.azure-devices.net/devices/sensor-01.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("hostname"), types.StringValue(strings.ToLower(hostname)))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("device_id"), types.StringValue(parts[0]))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), types.StringValue(common.ResourceID(hostname, "devices", parts[0])))...)
}

// ---- helpers ----------------------------------------------------------------

// keysInState reports whether hub-generated/plain keys are stored in state
// (true) or write-only arguments are in use for the respective slot (false).
func (m resourceModel) keysInState() (primary, secondary bool) {
	return m.PrimaryKeyWOVersion.IsNull(), m.SecondaryKeyWOVersion.IsNull()
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
	var diags diag.Diagnostics
	spec := client.DeviceSpec{
		DeviceID:     plan.DeviceID.ValueString(),
		Status:       plan.Status.ValueString(),
		StatusReason: plan.StatusReason.ValueString(),
		IotEdge:      plan.EdgeEnabled.ValueBool(),
		ParentScope:  plan.ParentScope.ValueString(),
	}
	auth, ok, d := authFromObject(ctx, plan.Authentication)
	diags.Append(d...)
	authType := client.AuthTypeSAS
	if ok && known(auth.Type) {
		authType = auth.Type.ValueString()
	} else if !ok && current != nil && current.Authentication != nil {
		// authentication omitted from config on an existing device: keep it.
		spec.Authentication = *current.Authentication
		return spec, diags
	}
	spec.Authentication = client.AuthenticationMechanism{Type: authType}
	switch authType {
	case client.AuthTypeSAS:
		var cur client.SymmetricKey
		if current != nil && current.Authentication != nil && current.Authentication.SymmetricKey != nil && current.Authentication.Type == client.AuthTypeSAS {
			cur = *current.Authentication.SymmetricKey
		}
		primary := chooseKey(auth.PrimaryKey, config.PrimaryKeyWO, cur.PrimaryKey)
		secondary := chooseKey(auth.SecondaryKey, config.SecondaryKeyWO, cur.SecondaryKey)
		// The service wants both keys or neither. Fill a missing counterpart
		// with a fresh random key rather than failing (a create with only
		// one key given, or a slot the hub never had).
		if (primary == "") != (secondary == "") {
			gen, err := common.NewSymmetricKey()
			if err != nil {
				diags.AddError("Cannot generate symmetric key", err.Error())
				return spec, diags
			}
			if primary == "" {
				primary = gen
			} else {
				secondary = gen
			}
		}
		if primary != "" {
			spec.Authentication.SymmetricKey = &client.SymmetricKey{PrimaryKey: primary, SecondaryKey: secondary}
		}
	case client.AuthTypeSelfSigned:
		spec.Authentication.X509Thumbprint = &client.X509Thumbprint{
			PrimaryThumbprint:   auth.PrimaryThumbprint.ValueString(),
			SecondaryThumbprint: auth.SecondaryThumbprint.ValueString(),
		}
	}
	return spec, diags
}

// chooseKey picks the key to send: explicit config, else write-only config,
// else what the hub currently holds ("" lets the hub generate on create).
func chooseKey(configured, writeOnly types.String, current string) string {
	if known(configured) {
		return configured.ValueString()
	}
	if known(writeOnly) {
		return writeOnly.ValueString()
	}
	return current
}

// writtenFromState reflects the prior state for conflict inspection.
func writtenFromState(state resourceModel, auth authModel) writtenFields {
	return writtenFields{
		Status:              state.Status.ValueString(),
		StatusReason:        state.StatusReason.ValueString(),
		IotEdge:             state.EdgeEnabled.ValueBool(),
		ParentScope:         state.ParentScope.ValueString(),
		AuthType:            auth.Type.ValueString(),
		PrimaryKey:          auth.PrimaryKey.ValueString(),
		SecondaryKey:        auth.SecondaryKey.ValueString(),
		PrimaryThumbprint:   auth.PrimaryThumbprint.ValueString(),
		SecondaryThumbprint: auth.SecondaryThumbprint.ValueString(),
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
	m.ParentScope = stringOrNull(parentScopeOf(d))
	auth := authFromHub(d.Authentication, true)
	if !primaryInState {
		auth.PrimaryKey = types.StringNull()
	}
	if !secondaryInState {
		auth.SecondaryKey = types.StringNull()
	}
	m.Authentication = auth.object()
	m.ETag = types.StringValue(d.ETag)
	m.GenerationID = types.StringValue(d.GenerationID)
	m.DeviceScope = stringOrNull(d.DeviceScope)
	scopes := make([]attr.Value, 0, len(d.ParentScopes))
	for _, s := range d.ParentScopes {
		scopes = append(scopes, types.StringValue(s))
	}
	list, diags := types.ListValue(types.StringType, scopes)
	m.ParentScopes = list
	m.ConnectionState = stringOrNull(d.ConnectionState)
	m.ConnectionStateUpdatedTime = stringOrNull(d.ConnectionStateUpdatedTime)
	m.LastActivityTime = stringOrNull(d.LastActivityTime)
	m.StatusUpdatedTime = stringOrNull(d.StatusUpdatedTime)
	m.CloudToDeviceMessageCount = types.Int64Value(d.CloudToDeviceMessageCount)
	return diags
}
