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

// planValidationTimeout bounds the plan-time testQueries call.
const planValidationTimeout = 30 * time.Second

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
			MarkdownDescription: "The `" + r.kind.idAttr() + "`. Also the import ID.",
			Computed:            true,
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
		r.kind.idAttr(): schema.StringAttribute{
			MarkdownDescription: strings.ToUpper(r.kind.noun()[:1]) + r.kind.noun()[1:] + " ID: " + idDescription + ". It must be unique among all " +
				"configurations and IoT Edge deployments of the hub. Changing it replaces the " + r.kind.noun() + ".",
			Required:      true,
			PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			Validators:    []validator.String{stringvalidator.RegexMatches(idPattern, "must be "+idDescription)},
		},
		"target_condition": schema.StringAttribute{
			MarkdownDescription: targetConditionDescription(r.kind),
			Required:            true,
			Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
		},
		"priority": schema.Int64Attribute{
			MarkdownDescription: priorityDescription(r.kind),
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
			MarkdownDescription: "Custom metrics: a map from metric name to an IoT Hub query, for example " + metricsExample(r.kind) + ". Results are in `metric_results`.",
			ElementType:         types.StringType,
			Optional:            true,
			Validators:          []validator.Map{mapvalidator.ValueStringsAre(stringvalidator.LengthAtLeast(1))},
		},
		"schema_version": schema.StringAttribute{
			MarkdownDescription: "Version string of the " + r.kind.noun() + " document as the hub reports it, if any. Tools such as the Azure CLI write `1.0`.",
			Computed:            true,
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
		"etag":              computed("ETag of the " + r.kind.noun() + "."),
		"created_time":      computed("Creation time."),
		"last_updated_time": computed("Last update time."),
		"system_metrics": schema.MapAttribute{
			MarkdownDescription: systemMetricsDescription(r.kind),
			ElementType:         types.Int64Type,
			Computed:            true,
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
			MarkdownDescription: "The `modulesContent` object of a deployment manifest as JSON, for example " +
				"`jsonencode(jsondecode(file(\"deployment.json\")).modulesContent)`. It holds `$edgeAgent` and `$edgeHub` with their " +
				"`properties.desired`, plus custom modules. A layered deployment carries `properties.desired.modules.<name>` entries " +
				"under `$edgeAgent`. **Changing it replaces the deployment**, and the hub re-evaluates every targeted device.",
			Required:      true,
			PlanModifiers: []planmodifier.String{jsondoc.RequiresReplaceIfChanged()},
		}
		description = "An IoT Edge deployment, including layered deployments. The hub applies the deployment manifest to every " +
			"IoT Edge device that matches `target_condition`, in order of `priority`. There is no separate flag for layered " +
			"deployments: a deployment is layered when its `$edgeAgent` content sets `properties.desired.modules.<name>` keys " +
			"instead of a full `properties.desired`.\n\n" +
			"`target_condition`, `priority`, `labels` and `metrics` can be changed in place. `target_condition` " +
			"and the `metrics` queries are checked against the hub at plan time. **Changing `modules_content` replaces the " +
			"deployment.** The content is compared by value, so reformatting it is not a change. A replacement deletes the " +
			"deployment and creates it again under the same ID. To avoid a window without a deployment, put a version in " +
			"`deployment_id` and use `lifecycle { create_before_destroy = true }`, as the example shows.\n\n" +
			"Destroying a deployment does not touch the devices. They keep the last applied manifest until another deployment " +
			"targets them."
	} else {
		attrs["device_content"] = schema.StringAttribute{
			CustomType: ContentType,
			MarkdownDescription: "Device twin desired properties to apply, as a JSON object of `properties.desired.<path>` keys, for " +
				"example `jsonencode({ \"properties.desired.firmware\" = { channel = \"stable\" } })`. Set exactly one of " +
				"`device_content` and `module_content`. **Changing it replaces the configuration**, and the hub re-evaluates every " +
				"targeted device.",
			Optional:      true,
			PlanModifiers: []planmodifier.String{jsondoc.RequiresReplaceIfChanged()},
			Validators:    []validator.String{stringvalidator.ExactlyOneOf(path.MatchRoot("device_content"), path.MatchRoot("module_content"))},
		}
		attrs["module_content"] = schema.StringAttribute{
			CustomType: ContentType,
			MarkdownDescription: "Module twin desired properties to apply, as a JSON object of `properties.desired.<path>` keys. Use it " +
				"with a module target condition such as `FROM devices.modules WHERE moduleId = '…'`. Set exactly one of `device_content` " +
				"and `module_content`. **Changing it replaces the configuration.**",
			Optional:      true,
			PlanModifiers: []planmodifier.String{jsondoc.RequiresReplaceIfChanged()},
		}
		description = "An automatic device management configuration. The hub applies its desired properties to every device or " +
			"module that matches `target_condition`, in order of `priority`.\n\n" +
			"`target_condition`, `priority`, `labels` and `metrics` can be changed in place. `target_condition` " +
			"and the `metrics` queries are checked against the hub at plan time. **Changing `device_content` or `module_content` " +
			"replaces the configuration.** The content is compared by value, so reformatting it is not a change. A replacement " +
			"deletes the configuration and creates it again under the same ID. To avoid a window without a configuration, put a " +
			"version in `configuration_id` and use `lifecycle { create_before_destroy = true }`.\n\n" +
			"Destroying a configuration does not touch the devices. The desired properties it applied stay in the twins until " +
			"another configuration or a twin resource changes them."
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: description,
		Attributes:          attrs,
		Blocks: map[string]schema.Block{
			"timeouts": timeouts.Block(ctx, common.TimeoutsOpts("20m")),
		},
	}
}

// IdentitySchema: configuration_id / deployment_id.
func (r *configResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	resp.IdentitySchema = identityschema.Schema{
		Attributes: map[string]identityschema.Attribute{
			r.kind.idAttr(): identityschema.StringAttribute{RequiredForImport: true, Description: "ID of the " + r.kind.noun() + "."},
		},
	}
}

func (k kind) setIdentity(ctx context.Context, id *tfsdk.ResourceIdentity, configID string) diag.Diagnostics {
	if id == nil {
		return nil
	}
	return id.SetAttribute(ctx, path.Root(k.idAttr()), types.StringValue(configID))
}

func (r *configResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	pd, diags := common.ExpectProviderData(req.ProviderData)
	resp.Diagnostics.Append(diags...)
	r.pd = pd
}

// ModifyPlan defers when the hub is not known yet, sets the ID, and validates
// target_condition / metrics against the hub when they change (fixed
// behaviour, CONCEPT.md §15 row 11).
func (r *configResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() { // destroy
		return
	}
	plan, diags := r.kind.get(ctx, req.Plan)
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
	if r.pd != nil {
		if _, ok, diags := r.pd.Hub(); !ok && !diags.HasError() && req.ClientCapabilities.DeferralAllowed {
			resp.Deferred = &resource.Deferred{Reason: resource.DeferredReasonProviderConfigUnknown}
		}
	}
	if !plan.ConfigurationID.IsUnknown() {
		plan.ID = plan.ConfigurationID
	}
	resp.Diagnostics.Append(r.kind.set(ctx, &resp.Plan, plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Plan-time validation is best effort: a short deadline keeps a throttled
	// hub from stalling the plan (the check degrades to a warning).
	vctx, cancel := context.WithTimeout(ctx, planValidationTimeout)
	defer cancel()
	resp.Diagnostics.Append(r.validateQueries(vctx, plan, state)...)
}

// validateQueries calls testQueries when the planned target condition or
// metric queries differ from state (always on create). Unknown values, an
// unknown hub and the `*` target (accepted by PUT, rejected by testQueries —
// verified) skip the check; transient failures become warnings.
func (r *configResource) validateQueries(ctx context.Context, plan model, state *model) diag.Diagnostics {
	var diags diag.Diagnostics
	if r.pd == nil || plan.TargetCondition.IsUnknown() || plan.Metrics.IsUnknown() {
		return diags
	}
	c, ok, d := r.pd.Hub()
	diags.Append(d...)
	if diags.HasError() || !ok {
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

	c, hostname, diags := r.client()
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
					spec.ID, hostname, spec.ID, common.DescribeError(err)))
			return
		}
		resp.Diagnostics.AddError("Cannot create IoT Hub "+r.kind.noun(), common.DescribeError(err))
		return
	}
	r.kind.fromHub(&plan, created, plan)
	resp.Diagnostics.Append(r.kind.set(ctx, &resp.State, plan)...)
	resp.Diagnostics.Append(r.kind.setIdentity(ctx, resp.Identity, created.ID)...)
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

	c, ok, diags := r.pd.Hub()
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !ok {
		// The hub is not known yet (first plan of a configuration that also
		// creates it); the prior state stands until apply.
		resp.Diagnostics.Append(r.kind.setIdentity(ctx, resp.Identity, state.ConfigurationID.ValueString())...)
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
	r.kind.fromHub(&state, cfg, state)
	resp.Diagnostics.Append(r.kind.set(ctx, &resp.State, state)...)
	resp.Diagnostics.Append(r.kind.setIdentity(ctx, resp.Identity, cfg.ID)...)
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

	c, _, diags := r.client()
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
	r.kind.fromHub(&plan, updated, plan)
	resp.Diagnostics.Append(r.kind.set(ctx, &resp.State, plan)...)
	resp.Diagnostics.Append(r.kind.setIdentity(ctx, resp.Identity, updated.ID)...)
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

	c, _, diags := r.client()
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := c.DeleteConfiguration(ctx, state.ConfigurationID.ValueString(), "*"); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Cannot delete IoT Hub "+r.kind.noun(), common.DescribeError(err))
	}
}

func (r *configResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id := strings.TrimSpace(req.ID)
	if id == "" && req.Identity != nil {
		var i types.String
		resp.Diagnostics.Append(req.Identity.GetAttribute(ctx, path.Root(r.kind.idAttr()), &i)...)
		if resp.Diagnostics.HasError() {
			return
		}
		id = i.ValueString()
	}
	if id == "" {
		resp.Diagnostics.AddError("Invalid import", "Provide the "+r.kind.noun()+" ID as the import ID (for example fw-channel-stable) or as the identity attribute `"+r.kind.idAttr()+"`.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(r.kind.idAttr()), types.StringValue(id))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), types.StringValue(id))...)
	resp.Diagnostics.Append(r.kind.setIdentity(ctx, resp.Identity, id)...)
}

// ---- helpers ------------------------------------------------------------------

// client returns the hub client and hostname for an apply-time operation.
func (r *configResource) client() (*client.Client, string, diag.Diagnostics) {
	c, diags := r.pd.HubOrError()
	if diags.HasError() {
		return nil, "", diags
	}
	return c, c.Hostname(), nil
}

// ---- kind-specific attribute texts ------------------------------------------

func targetConditionDescription(k kind) string {
	if k.isEdge() {
		return "Which IoT Edge devices the deployment targets. A query condition over `deviceId`, `tags` and `properties.reported`, " +
			"for example `tags.site = 'munich'`. Use `*` for all devices."
	}
	return "Which devices or modules the configuration targets. A query condition over `deviceId`, `tags` and `properties.reported`, " +
		"for example `tags.site = 'munich'`. Use `*` for all devices, or `FROM devices.modules WHERE …` to target modules."
}

func priorityDescription(k kind) string {
	if k.isEdge() {
		return "Priority, 0 or higher (default 0). Among base deployments that target the same device, the highest priority wins. " +
			"Layered deployments are applied on top of the base deployment, higher priority last, and must have a higher priority than the base."
	}
	return "Priority, 0 or higher (default 0). When several configurations target the same device, the highest priority wins."
}

func metricsExample(k kind) string {
	if k.isEdge() {
		return "`SELECT deviceId FROM devices.modules WHERE moduleId = '$edgeHub' AND properties.reported.lastDesiredStatus.code = 200`"
	}
	return "`SELECT deviceId FROM devices WHERE properties.reported.firmware.channel = 'stable'`"
}

func systemMetricsDescription(k kind) string {
	if k.isEdge() {
		return "Latest system metrics computed by the hub: `targetedCount`, `appliedCount`, `reportedSuccessfulCount` and " +
			"`reportedFailedCount`. Empty until the hub has evaluated the deployment."
	}
	return "Latest system metrics computed by the hub: `targetedCount` and `appliedCount`. Empty until the hub has evaluated the configuration."
}

// targetConditionSummary is the short data-source form of targetConditionDescription.
func targetConditionSummary(k kind) string {
	if k.isEdge() {
		return "Which IoT Edge devices the deployment targets, as a query condition."
	}
	return "Which devices or modules the configuration targets, as a query condition."
}
