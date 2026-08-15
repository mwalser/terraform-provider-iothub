package configuration

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/identityschema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/common"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/jsondoc"
)

var (
	_ resource.Resource                = &configResource{}
	_ resource.ResourceWithConfigure   = &configResource{}
	_ resource.ResourceWithImportState = &configResource{}
	_ resource.ResourceWithModifyPlan  = &configResource{}
	_ resource.ResourceWithIdentity    = &configResource{}
)

// NewConfigurationResource returns the iothub_configuration resource.
func NewConfigurationResource() resource.Resource { return &configResource{kind: configurationKind} }

// NewEdgeDeploymentResource returns the iothub_edge_deployment resource.
func NewEdgeDeploymentResource() resource.Resource { return &configResource{kind: edgeDeploymentKind} }

type configResource struct {
	kind kind
	pd   *common.ProviderData
}

const defaultTimeout = 20 * time.Minute

func (r *configResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + r.kind.typeSuffix()
}

func (r *configResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	computed := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{MarkdownDescription: desc, Computed: true}
	}
	attrs := map[string]schema.Attribute{
		"id": schema.StringAttribute{
			MarkdownDescription: "`<hostname>/configurations/<id>` — also the import ID.",
			Computed:            true,
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
		"hostname": schema.StringAttribute{
			MarkdownDescription: common.HostnameAttributeDescription + " Changing it replaces the resource.",
			Optional:            true,
			Computed:            true,
			Validators:          common.HostnameValidators(),
			PlanModifiers: []planmodifier.String{
				stringplanmodifier.UseStateForUnknown(),
				stringplanmodifier.RequiresReplace(),
			},
		},
		r.kind.idAttr(): schema.StringAttribute{
			MarkdownDescription: strings.ToUpper(r.kind.noun()[:1]) + r.kind.noun()[1:] + " ID: " + idDescription + ". Immutable.",
			Required:            true,
			PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			Validators:          []validator.String{stringvalidator.RegexMatches(idPattern, "must be "+idDescription)},
		},
		"target_condition": schema.StringAttribute{
			MarkdownDescription: "Which devices (or modules) the " + r.kind.noun() + " targets: a query condition over `deviceId`, `tags` and " +
				"`properties.reported` (e.g. `tags.site = 'munich'`), `*` for all devices, or `FROM devices.modules WHERE …` for module " +
				"targets.",
			Required:   true,
			Validators: []validator.String{stringvalidator.LengthAtLeast(1)},
		},
		"priority": schema.Int64Attribute{
			MarkdownDescription: "Priority (≥ 0, default 0). When several " + r.kind.noun() + "s target the same device, the highest priority wins.",
			Optional:            true,
			Computed:            true,
			Default:             int64default.StaticInt64(0),
			Validators:          []validator.Int64{int64validator.AtLeast(0)},
		},
		"labels": schema.MapAttribute{
			MarkdownDescription: "Free-form labels (string map).",
			ElementType:         types.StringType,
			Optional:            true,
		},
		"metrics": schema.MapAttribute{
			MarkdownDescription: "Custom metric queries, name → IoT Hub query (e.g. `SELECT deviceId FROM devices WHERE properties.reported.firmware.channel = 'stable'`). " +
				"Results are in `metric_results`.",
			ElementType: types.StringType,
			Optional:    true,
			Validators:  []validator.Map{mapvalidator.ValueStringsAre(stringvalidator.LengthAtLeast(1))},
		},
		"schema_version": schema.StringAttribute{
			MarkdownDescription: "Schema version of the configuration document (`1.0` is what the Azure CLI writes). Left as the hub reports it when omitted.",
			Optional:            true,
			Computed:            true,
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
		"etag":                  computed("ETag of the " + r.kind.noun() + "."),
		"created_time_utc":      computed("Creation time."),
		"last_updated_time_utc": computed("Last update time."),
		"system_metrics": schema.MapAttribute{
			MarkdownDescription: "Latest hub-computed system metrics: `targetedCount`, `appliedCount` (and for deployments `reportedSuccessfulCount`, " +
				"`reportedFailedCount`). Empty until the hub has evaluated the " + r.kind.noun() + ".",
			ElementType: types.Int64Type,
			Computed:    true,
		},
		"metric_results": schema.MapAttribute{
			MarkdownDescription: "Latest results of the custom `metrics`, by name.",
			ElementType:         types.Int64Type,
			Computed:            true,
		},
	}
	var description string
	if r.kind.isEdge() {
		attrs["modules_content"] = schema.StringAttribute{
			CustomType: ModulesContentType,
			MarkdownDescription: "The `modulesContent` object of a deployment manifest as JSON (`jsonencode(jsondecode(file(\"deployment.json\")).modulesContent)`): " +
				"`$edgeAgent` and `$edgeHub` with their `properties.desired`, plus custom modules; a layered deployment carries " +
				"`properties.desired.modules.<name>` entries under `$edgeAgent`. **Changing it replaces the deployment**, which " +
				"re-evaluates every targeted device.",
			Required:      true,
			PlanModifiers: []planmodifier.String{jsondoc.RequiresReplaceIfChanged()},
		}
		description = "An IoT Edge deployment, including layered deployments: the deployment manifest the hub applies to every edge " +
			"device matching `target_condition`, by `priority`.\n\n" +
			"`target_condition`, `priority`, `labels` and `metrics` can be changed in place; **`modules_content` cannot — changing " +
			"it replaces the deployment.**"
	} else {
		attrs["device_content"] = schema.StringAttribute{
			CustomType: ContentType,
			MarkdownDescription: "Device twin desired properties to apply, as a JSON object of `properties.desired.<path>` keys " +
				"(`jsonencode({ \"properties.desired.firmware\" = { channel = \"stable\" } })`). Exactly one of `device_content` and " +
				"`module_content`. **Changing it replaces the configuration**, which re-evaluates every targeted device.",
			Optional:      true,
			PlanModifiers: []planmodifier.String{jsondoc.RequiresReplaceIfChanged()},
			Validators:    []validator.String{stringvalidator.ExactlyOneOf(path.MatchRoot("device_content"), path.MatchRoot("module_content"))},
		}
		attrs["module_content"] = schema.StringAttribute{
			CustomType: ContentType,
			MarkdownDescription: "Module twin desired properties to apply (`properties.desired.<path>` keys); use with a module target " +
				"condition (`FROM devices.modules WHERE moduleId = '…'`). Exactly one of `device_content` and `module_content`. " +
				"**Changing it replaces the configuration.**",
			Optional:      true,
			PlanModifiers: []planmodifier.String{jsondoc.RequiresReplaceIfChanged()},
		}
		description = "An automatic device management configuration: desired properties the hub applies to every device or " +
			"module matching `target_condition`, by `priority`.\n\n" +
			"`target_condition`, `priority`, `labels` and `metrics` can be changed in place; **the content cannot — changing it " +
			"replaces the configuration.**"
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: description,
		Attributes:          attrs,
		Blocks: map[string]schema.Block{
			"timeouts": timeouts.Block(ctx, timeouts.Opts{Create: true, Read: true, Update: true, Delete: true}),
		},
	}
}

// IdentitySchema: hub + configuration_id / deployment_id.
func (r *configResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	resp.IdentitySchema = identityschema.Schema{
		Attributes: map[string]identityschema.Attribute{
			"hostname":      identityschema.StringAttribute{RequiredForImport: true, Description: "IoT Hub hostname."},
			r.kind.idAttr(): identityschema.StringAttribute{RequiredForImport: true, Description: "ID of the " + r.kind.noun() + "."},
		},
	}
}

func (k kind) setIdentity(ctx context.Context, id *tfsdk.ResourceIdentity, hostname, configID string) diag.Diagnostics {
	if id == nil {
		return nil
	}
	var diags diag.Diagnostics
	diags.Append(id.SetAttribute(ctx, path.Root("hostname"), types.StringValue(hostname))...)
	diags.Append(id.SetAttribute(ctx, path.Root(k.idAttr()), types.StringValue(configID))...)
	return diags
}

func (r *configResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	pd, diags := common.ExpectProviderData(req.ProviderData)
	resp.Diagnostics.Append(diags...)
	r.pd = pd
}

// ModifyPlan resolves the hostname and validates target_condition / metrics
// against the hub when they change (fixed behaviour, CONCEPT.md §15 row 11).
func (r *configResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() { // destroy
		return
	}
	plan, diags := r.kind.get(ctx, req.Plan)
	resp.Diagnostics.Append(diags...)
	config, diags := r.kind.get(ctx, req.Config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	var state *model
	if !req.State.Raw.IsNull() {
		s, diags := r.kind.get(ctx, req.State)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		state = &s
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
	if !plan.Hostname.IsUnknown() && !plan.ConfigurationID.IsUnknown() {
		plan.ID = types.StringValue(resourceID(plan.Hostname.ValueString(), plan.ConfigurationID.ValueString()))
	}
	resp.Diagnostics.Append(r.kind.set(ctx, &resp.Plan, plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(r.validateQueries(ctx, plan, state)...)
}

// validateQueries calls testQueries when the planned target condition or
// metric queries differ from state (always on create). Unknown values, an
// unknown hostname and the `*` target (accepted by PUT, rejected by
// testQueries — verified) skip the check; transient failures become warnings.
func (r *configResource) validateQueries(ctx context.Context, plan model, state *model) diag.Diagnostics {
	var diags diag.Diagnostics
	if r.pd == nil || plan.Hostname.IsUnknown() || plan.Hostname.IsNull() || plan.TargetCondition.IsUnknown() || plan.Metrics.IsUnknown() {
		return diags
	}
	target := plan.TargetCondition.ValueString()
	metrics, d := mapToGo(ctx, plan.Metrics)
	diags.Append(d...)
	if diags.HasError() {
		return diags
	}
	for _, v := range plan.Metrics.Elements() {
		if v.IsUnknown() {
			return diags
		}
	}
	changed := state == nil
	if state != nil {
		prior, _ := mapToGo(ctx, state.Metrics)
		changed = state.TargetCondition.ValueString() != target || !equalStringMaps(prior, metrics)
	}
	if !changed {
		return diags
	}
	if target == "*" {
		if len(metrics) == 0 {
			return diags
		}
		target = "deviceId != ''" // any valid condition: only the metric queries are checked
	}
	c, d := r.pd.ClientFor(ctx, plan.Hostname.ValueString())
	diags.Append(d...)
	if diags.HasError() {
		return diags
	}
	res, err := c.TestConfigurationQueries(ctx, target, metrics)
	if err != nil {
		diags.AddWarning("Could not validate the "+r.kind.noun()+" queries during plan",
			"The hub could not check `target_condition` / `metrics` now; they will be validated when the "+r.kind.noun()+" is written.\n\n"+common.DescribeError(err))
		return diags
	}
	if res.TargetConditionError != "" && plan.TargetCondition.ValueString() != "*" {
		diags.AddAttributeError(path.Root("target_condition"), "Invalid target condition", res.TargetConditionError)
	}
	for name, msg := range res.CustomMetricQueryErrors {
		diags.AddAttributeError(path.Root("metrics").AtMapKey(name), "Invalid metric query", msg)
	}
	return diags
}

func (r *configResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	plan, diags := r.kind.get(ctx, req.Plan)
	resp.Diagnostics.Append(diags...)
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
	spec, diags := r.kind.spec(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	tflog.Debug(ctx, "creating IoT Hub "+r.kind.noun(), map[string]any{"hostname": hostname, "id": spec.ID})
	created, err := c.CreateConfiguration(ctx, spec)
	if err != nil {
		if client.IsConflict(err) {
			resp.Diagnostics.AddAttributeError(path.Root(r.kind.idAttr()), strings.ToUpper(r.kind.noun()[:1])+r.kind.noun()[1:]+" already exists",
				fmt.Sprintf("A configuration or deployment with ID %q already exists in %s. To manage it with Terraform, import it:\n\n  terraform import <address> %s\n\n%s",
					spec.ID, hostname, resourceID(hostname, spec.ID), common.DescribeError(err)))
			return
		}
		resp.Diagnostics.AddError("Cannot create IoT Hub "+r.kind.noun(), common.DescribeError(err))
		return
	}
	r.kind.fromHub(&plan, hostname, created, plan)
	resp.Diagnostics.Append(r.kind.set(ctx, &resp.State, plan)...)
	resp.Diagnostics.Append(r.kind.setIdentity(ctx, resp.Identity, hostname, created.ID)...)
}

func (r *configResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	state, diags := r.kind.get(ctx, req.State)
	resp.Diagnostics.Append(diags...)
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
	cfg, err := c.GetConfiguration(ctx, state.ConfigurationID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			tflog.Info(ctx, "IoT Hub "+r.kind.noun()+" no longer exists; removing from state", map[string]any{"id": state.ConfigurationID.ValueString()})
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Cannot read IoT Hub "+r.kind.noun(), common.DescribeError(err))
		return
	}
	resp.Diagnostics.Append(r.checkKind(cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.kind.fromHub(&state, hostname, cfg, state)
	resp.Diagnostics.Append(r.kind.set(ctx, &resp.State, state)...)
	resp.Diagnostics.Append(r.kind.setIdentity(ctx, resp.Identity, hostname, cfg.ID)...)
}

// checkKind refuses to manage a deployment as a configuration or vice versa.
func (r *configResource) checkKind(cfg *client.Configuration) diag.Diagnostics {
	var diags diag.Diagnostics
	if edge, section := contentKind(cfg); edge != r.kind.isEdge() {
		other := configurationKind
		if edge {
			other = edgeDeploymentKind
		}
		diags.AddError("Wrong resource type for this "+r.kind.noun(),
			fmt.Sprintf("%q carries %s and is an %s; manage it with %s instead.", cfg.ID, section, other.noun(), other.resourceType()))
	}
	return diags
}

func (r *configResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	plan, diags := r.kind.get(ctx, req.Plan)
	resp.Diagnostics.Append(diags...)
	state, d := r.kind.get(ctx, req.State)
	resp.Diagnostics.Append(d...)
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
	spec, diags := r.kind.spec(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := spec.ID
	etag := state.ETag.ValueString()
	var updated *client.Configuration
	for attempt := 1; ; attempt++ {
		var err error
		updated, err = c.UpdateConfiguration(ctx, spec, etag)
		if err == nil {
			break
		}
		if client.IsNotFound(err) {
			resp.Diagnostics.AddError(strings.ToUpper(r.kind.noun()[:1])+r.kind.noun()[1:]+" no longer exists",
				fmt.Sprintf("%q was deleted outside Terraform. Run `terraform plan` again; it will be re-created.", id))
			return
		}
		if !client.IsPreconditionFailed(err) || attempt >= 3 {
			resp.Diagnostics.AddError("Cannot update IoT Hub "+r.kind.noun(), common.DescribeError(err))
			return
		}
		// 412: somebody wrote since the last refresh. Compare what we write
		// against the plan's baseline (prior state); retry only when the
		// fields Terraform manages are untouched (CONCEPT.md §11.1).
		fresh, gerr := c.GetConfiguration(ctx, id)
		if gerr != nil {
			resp.Diagnostics.AddError("Cannot read IoT Hub "+r.kind.noun()+" after a concurrent change", common.DescribeError(gerr))
			return
		}
		if changed := diffWritten(writtenFromModel(ctx, state), writtenFromHub(fresh)); len(changed) > 0 {
			resp.Diagnostics.AddError(strings.ToUpper(r.kind.noun()[:1])+r.kind.noun()[1:]+" changed outside Terraform",
				fmt.Sprintf("%q was modified since the last refresh:\n  - %s\n\nRun `terraform plan` again to review the external change before applying.", id, strings.Join(changed, "\n  - ")))
			return
		}
		tflog.Debug(ctx, "412 without a change to written fields; retrying with the fresh ETag", map[string]any{"id": id, "attempt": attempt})
		etag = fresh.ETag
	}
	r.kind.fromHub(&plan, hostname, updated, plan)
	resp.Diagnostics.Append(r.kind.set(ctx, &resp.State, plan)...)
	resp.Diagnostics.Append(r.kind.setIdentity(ctx, resp.Identity, hostname, updated.ID)...)
}

func (r *configResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	state, diags := r.kind.get(ctx, req.State)
	resp.Diagnostics.Append(diags...)
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
	if err := c.DeleteConfiguration(ctx, state.ConfigurationID.ValueString(), "*"); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Cannot delete IoT Hub "+r.kind.noun(), common.DescribeError(err))
	}
}

func (r *configResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	var hostname, id string
	if req.ID != "" {
		host, parts, err := common.ParseResourceID(req.ID, "configurations")
		if err != nil || len(parts) != 1 {
			resp.Diagnostics.AddError("Invalid import ID", "Expected `<hostname>/configurations/<id>`, e.g. contoso.azure-devices.net/configurations/fw-channel-stable.")
			return
		}
		hostname, id = host, parts[0]
	} else if req.Identity != nil {
		var h, i types.String
		resp.Diagnostics.Append(req.Identity.GetAttribute(ctx, path.Root("hostname"), &h)...)
		resp.Diagnostics.Append(req.Identity.GetAttribute(ctx, path.Root(r.kind.idAttr()), &i)...)
		if resp.Diagnostics.HasError() {
			return
		}
		hostname, id = h.ValueString(), i.ValueString()
	}
	if hostname == "" || id == "" {
		resp.Diagnostics.AddError("Invalid import", "Provide the import ID `<hostname>/configurations/<id>` or the identity attributes `hostname` and `"+r.kind.idAttr()+"`.")
		return
	}
	hostname = strings.ToLower(hostname)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("hostname"), types.StringValue(hostname))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(r.kind.idAttr()), types.StringValue(id))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), types.StringValue(resourceID(hostname, id)))...)
	resp.Diagnostics.Append(r.kind.setIdentity(ctx, resp.Identity, hostname, id)...)
}

// ---- helpers ------------------------------------------------------------------

func resourceID(hostname, id string) string { return common.ResourceID(hostname, "configurations", id) }

func (r *configResource) client(ctx context.Context, hostname types.String) (*client.Client, string, diag.Diagnostics) {
	var diags diag.Diagnostics
	if hostname.IsUnknown() || hostname.IsNull() || hostname.ValueString() == "" {
		diags.AddAttributeError(path.Root("hostname"), "IoT Hub hostname unknown at apply time", "Set `hostname` on the resource or on the provider block.")
		return nil, "", diags
	}
	c, d := r.pd.ClientFor(ctx, hostname.ValueString())
	diags.Append(d...)
	if diags.HasError() {
		return nil, "", diags
	}
	return c, c.Hostname(), nil
}
