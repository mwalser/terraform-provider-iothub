package configuration

import (
	"context"
	"fmt"
	"strings"
	"time"

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
	"github.com/mwalser/terraform-provider-iothub/internal/provider/secrets"
	"github.com/mwalser/terraform-provider-iothub/internal/twinpatch"
)

var (
	_ resource.Resource                   = &configResource{}
	_ resource.ResourceWithConfigure      = &configResource{}
	_ resource.ResourceWithImportState    = &configResource{}
	_ resource.ResourceWithModifyPlan     = &configResource{}
	_ resource.ResourceWithIdentity       = &configResource{}
	_ resource.ResourceWithValidateConfig = &configResource{}
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
			MarkdownDescription: "Free-form labels.",
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
			MarkdownDescription: "Schema version of the document as reported by the hub, if any.",
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
			MarkdownDescription: "The deployment manifest's `modulesContent` object as JSON. A base deployment contains full " +
				"`$edgeAgent` and `$edgeHub` desired properties, plus any custom-module desired properties. A layered deployment " +
				"contains patches under `$edgeAgent` and, when routes are present, `$edgeHub`. String values that exactly match " +
				"`{{secret.<name>}}` are placeholders for `secrets_wo`. " +
				"**Changing it replaces the deployment.**",
			Required: true,
		}
		attrs["secrets_wo"] = schema.MapAttribute{
			MarkdownDescription: "Secrets used by `modules_content`, keyed by name (" + secrets.NameDescription + "). A string value " +
				"exactly matching `{{secret.<name>}}` is replaced before the deployment is sent. Every placeholder must have a matching " +
				"entry, and every entry must be used. " +
				"**Changing a value, or using one that is not known until apply, replaces the deployment.**",
			ElementType: types.StringType,
			Optional:    true,
			WriteOnly:   true,
			Sensitive:   true,
			Validators:  secrets.MapValidators(),
		}
		attrs["secrets_fingerprint"] = schema.StringAttribute{
			MarkdownDescription: "Opaque comparison value maintained by the provider for the secrets behind the placeholders.",
			Computed:            true,
		}
		description = "An IoT Edge deployment, including layered deployments. The hub applies the deployment manifest to every " +
			"IoT Edge device that matches `target_condition`, in order of `priority`. A deployment is layered when its " +
			"`$edgeAgent` content sets `properties.desired.modules.<name>` keys instead of a full `properties.desired`.\n\n" +
			"-> You can build `modules_content` with `provider::iothub::edge_manifest` instead of reading it from a " +
			"file.\n\n" +
			"**Changing `modules_content` replaces the deployment.** Reformatting the JSON changes only Terraform state because " +
			"content is compared by value. To avoid a window without a deployment, use a versioned `deployment_id` with " +
			"`lifecycle { create_before_destroy = true }`."
	} else {
		attrs["device_content"] = schema.StringAttribute{
			CustomType: ContentType,
			MarkdownDescription: "Device twin desired properties to apply, as a JSON object of `properties.desired.<path>` keys, for " +
				"example `jsonencode({ \"properties.desired.firmware\" = { channel = \"stable\" } })`. Set exactly one of " +
				"`device_content` and `module_content`. **Changing it replaces the configuration.**",
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
			"**Changing `device_content` or `module_content` replaces the configuration.** Reformatting the JSON changes only " +
			"Terraform state because content is compared by value. To avoid a window without a configuration, use a versioned " +
			"`configuration_id` with `lifecycle { create_before_destroy = true }`."
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: description,
		Attributes:          attrs,
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

// ValidateConfig checks the secret placeholders of an edge deployment
// against secrets_wo: every placeholder needs a key and every key a
// placeholder (CONCEPT.md §6.7).
func (r *configResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	if !r.kind.isEdge() {
		return
	}
	cfg, diags := r.kind.get(ctx, req.Config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(secrets.Validate(cfg.ModulesContent, cfg.SecretsWO, "secrets_wo")...)
}

// ModifyPlan defers when the hub is not known yet, sets the ID, decides on
// the replacement of an edge deployment from its secrets fingerprint
// (planModulesContent, CONCEPT.md §6.7) and validates target_condition /
// metrics against the hub when they change (fixed behaviour, §14 row 11).
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
	common.DeferIfHubUnknown(r.pd, req, resp)
	if !plan.ConfigurationID.IsUnknown() {
		plan.ID = plan.ConfigurationID
	}
	if r.kind.isEdge() {
		values, known, d := r.secretValues(ctx, req.Config)
		resp.Diagnostics.Append(d...)
		if resp.Diagnostics.HasError() {
			return
		}
		blame, d := planModulesContent(plan, state, values, known)
		resp.Diagnostics.Append(d...)
		if blame == "secrets_fingerprint" {
			plan.SecretsFingerprint = types.StringUnknown() // the plan shows the replacement here
		}
		if blame != "" {
			resp.RequiresReplace = append(resp.RequiresReplace, path.Root(blame))
		}
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

// ---- secrets (CONCEPT.md §6.7) ----------------------------------------------

// planModulesContent decides whether an existing edge deployment is
// replaced (CONCEPT.md §6.7), returning the attribute to blame: when the
// document changed, or when a secret behind a placeholder is not what the
// hub held at the last refresh. It stands in for the jsondoc plan modifier
// the configuration resource uses, because the comparison needs the
// write-only secrets, which only the configuration carries. Documents are
// compared after resolving both with the configured values: state normally
// holds placeholders, an imported deployment holds the hub's values. When
// they agree, the values are compared with the fingerprint the last refresh
// or write took of the hub's — no hub read at plan time.
func planModulesContent(plan model, state *model, values map[string]string, known bool) (blame string, diags diag.Diagnostics) {
	if state == nil { // create
		return "", diags
	}
	if !plan.ConfigurationID.IsUnknown() && plan.ConfigurationID.ValueString() != state.ConfigurationID.ValueString() {
		return "", diags // the ID already forces replacement (the versioned-ID pattern); nothing to add
	}
	if plan.ModulesContent.IsUnknown() || state.ModulesContent.IsNull() || state.ModulesContent.IsUnknown() {
		return "modules_content", diags // cannot tell yet
	}
	planDoc, err := plan.ModulesContent.Object()
	if err != nil {
		return "", diags // reported by the attribute's validation
	}
	stateDoc, err := state.ModulesContent.Object()
	if err != nil {
		return "modules_content", diags // the hub's document kept verbatim by content(); a replacement rewrites it
	}
	// (1) Both documents with the known values filled in, state's
	// placeholders renamed to the plan's where both hold one: every
	// non-secret change and, on an import (the hub's values in state, no
	// placeholders), a configured secret that differs from the imported one.
	stateDoc = secrets.AdoptTokens(stateDoc, planDoc)
	if !twinpatch.Equal(secrets.Resolve(planDoc, values), secrets.Resolve(stateDoc, values)) {
		return "modules_content", diags
	}
	refs := secrets.Refs(planDoc)
	if len(refs) == 0 || len(secrets.Refs(stateDoc)) == 0 {
		return "", diags // no placeholders, or an import: (1) compared every value
	}
	if !known {
		diags.AddWarning("IoT Edge deployment secrets not known until apply",
			fmt.Sprintf("Values of `secrets_wo` for %q are not known until apply, so whether they changed cannot be checked; the deployment is replaced.", state.ConfigurationID.ValueString()))
		return "secrets_fingerprint", diags
	}
	// (2) Only a placeholder state already carried is in the fingerprint;
	// one that replaces a literal was just compared by value above.
	var carried []secrets.Ref
	for _, r := range refs {
		if !r.IsLiteral(stateDoc) {
			carried = append(carried, r)
		}
	}
	if len(carried) > 0 && !secrets.ParseFingerprint(state.SecretsFingerprint.ValueString()).Matches(carried, values) {
		return "secrets_fingerprint", diags // rotated in the configuration, or recreated on the hub before the last refresh
	}
	return "", diags
}

// secretValues reads the write-only secrets of an edge deployment from the
// configuration, the only place Terraform carries them. Create and Update
// ignore known: at apply every value is known.
func (r *configResource) secretValues(ctx context.Context, config tfsdk.Config) (values map[string]string, known bool, diags diag.Diagnostics) {
	if !r.kind.isEdge() {
		return nil, true, nil
	}
	cfg, diags := r.kind.get(ctx, config)
	if diags.HasError() {
		return nil, false, diags
	}
	values, known = secrets.Values(cfg.SecretsWO)
	return values, known, diags
}

func (r *configResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	plan, diags := r.kind.get(ctx, req.Plan)
	resp.Diagnostics.Append(diags...)
	values, _, diags := r.secretValues(ctx, req.Config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, diags := r.pd.HubOrError()
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	hostname := c.Hostname()
	spec, diags := r.kind.spec(ctx, plan, values)
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
	r.kind.absorbHub(&plan, types.StringNull(), created) // no prior fingerprint: a fresh salt
	resp.Diagnostics.Append(r.kind.set(ctx, &resp.State, plan)...)
	resp.Diagnostics.Append(r.kind.setIdentity(ctx, resp.Identity, created.ID)...)
}

func (r *configResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	state, diags := r.kind.get(ctx, req.State)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
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
			// The identity is returned even though the object is gone: the
			// framework rejects a read without one, and Terraform before 1.12
			// (or state written by it) supplies none to fall back on.
			resp.Diagnostics.Append(r.kind.setIdentity(ctx, resp.Identity, state.ConfigurationID.ValueString())...)
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
	r.kind.absorbHub(&state, state.SecretsFingerprint, cfg)
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
			fmt.Sprintf("%q carries %s and is %s; manage it with %s instead.", cfg.ID, section, other.indefiniteNoun(), other.resourceType()))
	}
	return diags
}

func (r *configResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	plan, diags := r.kind.get(ctx, req.Plan)
	resp.Diagnostics.Append(diags...)
	state, d := r.kind.get(ctx, req.State)
	resp.Diagnostics.Append(d...)
	values, _, d := r.secretValues(ctx, req.Config)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	state.ModulesContent = adoptPlanTokens(state.ModulesContent, plan.ModulesContent)
	// Terraform sees a changed configured string even when its JSON value is
	// equal — a reformatted document, placeholders replacing the values an
	// import brought in, or a renamed placeholder. Record that representation
	// in state without spending a configuration write or moving the remote
	// ETag.
	if len(diffWritten(writtenFromModel(ctx, state, values), writtenFromModel(ctx, plan, values))) == 0 {
		resolved, err := resolvedRaw(plan.ModulesContent, values)
		if err != nil { // cannot happen: the plan compared the same resolution
			resp.Diagnostics.Append(secrets.Unresolved(err))
			return
		}
		plan.SecretsFingerprint = fingerprint(plan.ModulesContent, state.SecretsFingerprint, resolved)
		plan.SchemaVersion = state.SchemaVersion
		plan.ETag = state.ETag
		plan.CreatedTime = state.CreatedTime
		plan.LastUpdatedTime = state.LastUpdatedTime
		plan.SystemMetrics = state.SystemMetrics
		plan.MetricResults = state.MetricResults
		resp.Diagnostics.Append(r.kind.set(ctx, &resp.State, plan)...)
		resp.Diagnostics.Append(r.kind.setIdentity(ctx, resp.Identity, plan.ConfigurationID.ValueString())...)
		return
	}
	c, diags := r.pd.HubOrError()
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	spec, diags := r.kind.spec(ctx, plan, values)
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
				fmt.Sprintf("%q was deleted outside Terraform. Run `terraform plan` again.", id))
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
		if changed := diffWritten(writtenFromModel(ctx, state, values), writtenFromHub(fresh)); len(changed) > 0 {
			resp.Diagnostics.AddError(strings.ToUpper(r.kind.noun()[:1])+r.kind.noun()[1:]+" changed outside Terraform",
				fmt.Sprintf("%q was modified since the last refresh:\n  - %s\n\nRun `terraform plan` again to review the external change before applying.", id, strings.Join(changed, "\n  - ")))
			return
		}
		tflog.Debug(ctx, "412 without a change to written fields; retrying with the fresh ETag", map[string]any{"id": id, "attempt": attempt})
		etag = fresh.ETag
	}
	r.kind.absorbHub(&plan, state.SecretsFingerprint, updated)
	resp.Diagnostics.Append(r.kind.set(ctx, &resp.State, plan)...)
	resp.Diagnostics.Append(r.kind.setIdentity(ctx, resp.Identity, updated.ID)...)
}

func (r *configResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	state, diags := r.kind.get(ctx, req.State)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, diags := r.pd.HubOrError()
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
	return "Priority, 0 or higher (default 0). When several configurations target the same twin path, the highest priority wins for that path."
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
