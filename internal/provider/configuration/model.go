// Package configuration implements iothub_configuration (automatic device /
// module management) and iothub_edge_deployment (IoT Edge deployments,
// including layered ones): CONCEPT.md §6.4 and §6.5. Both are backed by
// /configurations/{id}; they differ in the content shape and its validation,
// so they share one implementation parameterised by kind.
package configuration

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/jsondoc"
	"github.com/mwalser/terraform-provider-iothub/internal/twinpatch"
)

// idPattern is the configuration ID charset (verified: lowercase letters,
// digits and - + % _ * ! ' up to 128 characters; anything else is rejected).
var idPattern = regexp.MustCompile(`^[a-z0-9\-+%_*!']{1,128}$`)

const idDescription = "1 to 128 characters from `a-z 0-9 - + % _ * ! '`, lowercase only"

// kind distinguishes the two resources.
type kind int

const (
	configurationKind kind = iota
	edgeDeploymentKind
)

func (k kind) isEdge() bool { return k == edgeDeploymentKind }

func (k kind) noun() string {
	if k.isEdge() {
		return "IoT Edge deployment"
	}
	return "configuration"
}

func (k kind) typeSuffix() string {
	if k.isEdge() {
		return "_edge_deployment"
	}
	return "_configuration"
}

func (k kind) idAttr() string {
	if k.isEdge() {
		return "deployment_id"
	}
	return "configuration_id"
}

func (k kind) resourceType() string { return "iothub" + k.typeSuffix() }

// ---- content types ------------------------------------------------------------

// ContentType is the type of device_content / module_content: a JSON object
// whose top-level keys are twin desired-property paths
// (`properties.desired[.<path>]`; verified: anything else is rejected with
// InvalidConfigurationContent).
var ContentType = jsondoc.Type{Name: "configuration_content", Validate: func(doc map[string]any) []string {
	var out []string
	for _, k := range sortedKeys(doc) {
		if k != "properties.desired" && !strings.HasPrefix(k, "properties.desired.") {
			out = append(out, fmt.Sprintf("key %q: content keys must be `properties.desired` or start with `properties.desired.` (the service rejects anything else)", k))
		}
	}
	return out
}}

// ModulesContentType is the type of modules_content: the `modulesContent`
// object of an IoT Edge deployment manifest — one key per module (`$edgeAgent`
// is mandatory, verified) mapping to an object of desired-property paths.
var ModulesContentType = jsondoc.Type{Name: "modules_content", Validate: func(doc map[string]any) []string {
	var out []string
	if _, ok := doc["$edgeAgent"]; !ok {
		out = append(out, "modules_content must contain `$edgeAgent` (the service rejects deployments without it; layered deployments carry their `properties.desired.modules.<name>` entries under `$edgeAgent` too)")
	}
	for _, k := range sortedKeys(doc) {
		obj, isObj := doc[k].(map[string]any)
		if !isObj {
			out = append(out, fmt.Sprintf("module %q: value must be an object of desired-property paths (`properties.desired`, `properties.desired.modules.<name>`, …)", k))
			continue
		}
		for _, p := range sortedKeys(obj) {
			if p != "properties.desired" && !strings.HasPrefix(p, "properties.desired.") {
				out = append(out, fmt.Sprintf("module %q, key %q: keys must be `properties.desired` or start with `properties.desired.`", k, p))
			}
		}
	}
	return out
}}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---- models -------------------------------------------------------------------

// model is the kind-independent view of both resource schemas.
type model struct {
	ID              types.String
	ConfigurationID types.String // configuration_id / deployment_id
	TargetCondition types.String
	Priority        types.Int64
	Labels          types.Map
	DeviceContent   jsondoc.Value // configuration only
	ModuleContent   jsondoc.Value // configuration only
	ModulesContent  jsondoc.Value // edge deployment only
	Metrics         types.Map
	SchemaVersion   types.String
	ETag            types.String
	CreatedTime     types.String
	LastUpdatedTime types.String
	SystemMetrics   types.Map
	MetricResults   types.Map
	Timeouts        timeouts.Value
}

type configurationModel struct {
	ID              types.String   `tfsdk:"id"`
	ConfigurationID types.String   `tfsdk:"configuration_id"`
	TargetCondition types.String   `tfsdk:"target_condition"`
	Priority        types.Int64    `tfsdk:"priority"`
	Labels          types.Map      `tfsdk:"labels"`
	DeviceContent   jsondoc.Value  `tfsdk:"device_content"`
	ModuleContent   jsondoc.Value  `tfsdk:"module_content"`
	Metrics         types.Map      `tfsdk:"metrics"`
	SchemaVersion   types.String   `tfsdk:"schema_version"`
	ETag            types.String   `tfsdk:"etag"`
	CreatedTime     types.String   `tfsdk:"created_time"`
	LastUpdatedTime types.String   `tfsdk:"last_updated_time"`
	SystemMetrics   types.Map      `tfsdk:"system_metrics"`
	MetricResults   types.Map      `tfsdk:"metric_results"`
	Timeouts        timeouts.Value `tfsdk:"timeouts"`
}

type edgeDeploymentModel struct {
	ID              types.String   `tfsdk:"id"`
	DeploymentID    types.String   `tfsdk:"deployment_id"`
	TargetCondition types.String   `tfsdk:"target_condition"`
	Priority        types.Int64    `tfsdk:"priority"`
	Labels          types.Map      `tfsdk:"labels"`
	ModulesContent  jsondoc.Value  `tfsdk:"modules_content"`
	Metrics         types.Map      `tfsdk:"metrics"`
	SchemaVersion   types.String   `tfsdk:"schema_version"`
	ETag            types.String   `tfsdk:"etag"`
	CreatedTime     types.String   `tfsdk:"created_time"`
	LastUpdatedTime types.String   `tfsdk:"last_updated_time"`
	SystemMetrics   types.Map      `tfsdk:"system_metrics"`
	MetricResults   types.Map      `tfsdk:"metric_results"`
	Timeouts        timeouts.Value `tfsdk:"timeouts"`
}

type getter interface {
	Get(context.Context, any) diag.Diagnostics
}

type setter interface {
	Set(context.Context, any) diag.Diagnostics
}

func (k kind) get(ctx context.Context, src getter) (model, diag.Diagnostics) {
	if k.isEdge() {
		var e edgeDeploymentModel
		diags := src.Get(ctx, &e)
		return model{ID: e.ID, ConfigurationID: e.DeploymentID, TargetCondition: e.TargetCondition, Priority: e.Priority,
			Labels: e.Labels, DeviceContent: jsondoc.NewNull(ContentType), ModuleContent: jsondoc.NewNull(ContentType), ModulesContent: e.ModulesContent,
			Metrics: e.Metrics, SchemaVersion: e.SchemaVersion, ETag: e.ETag, CreatedTime: e.CreatedTime, LastUpdatedTime: e.LastUpdatedTime,
			SystemMetrics: e.SystemMetrics, MetricResults: e.MetricResults, Timeouts: e.Timeouts}, diags
	}
	var c configurationModel
	diags := src.Get(ctx, &c)
	return model{ID: c.ID, ConfigurationID: c.ConfigurationID, TargetCondition: c.TargetCondition, Priority: c.Priority,
		Labels: c.Labels, DeviceContent: c.DeviceContent, ModuleContent: c.ModuleContent, ModulesContent: jsondoc.NewNull(ModulesContentType),
		Metrics: c.Metrics, SchemaVersion: c.SchemaVersion, ETag: c.ETag, CreatedTime: c.CreatedTime, LastUpdatedTime: c.LastUpdatedTime,
		SystemMetrics: c.SystemMetrics, MetricResults: c.MetricResults, Timeouts: c.Timeouts}, diags
}

func (k kind) set(ctx context.Context, dst setter, m model) diag.Diagnostics {
	if k.isEdge() {
		return dst.Set(ctx, &edgeDeploymentModel{ID: m.ID, DeploymentID: m.ConfigurationID, TargetCondition: m.TargetCondition,
			Priority: m.Priority, Labels: m.Labels, ModulesContent: m.ModulesContent, Metrics: m.Metrics, SchemaVersion: m.SchemaVersion, ETag: m.ETag,
			CreatedTime: m.CreatedTime, LastUpdatedTime: m.LastUpdatedTime, SystemMetrics: m.SystemMetrics, MetricResults: m.MetricResults,
			Timeouts: m.Timeouts})
	}
	return dst.Set(ctx, &configurationModel{ID: m.ID, ConfigurationID: m.ConfigurationID, TargetCondition: m.TargetCondition,
		Priority: m.Priority, Labels: m.Labels, DeviceContent: m.DeviceContent, ModuleContent: m.ModuleContent, Metrics: m.Metrics,
		SchemaVersion: m.SchemaVersion, ETag: m.ETag, CreatedTime: m.CreatedTime, LastUpdatedTime: m.LastUpdatedTime,
		SystemMetrics: m.SystemMetrics, MetricResults: m.MetricResults, Timeouts: m.Timeouts})
}

// ---- hub <-> model ---------------------------------------------------------------

// contentKind tells which section a hub configuration carries.
func contentKind(c *client.Configuration) (edge bool, section string) {
	switch {
	case len(c.Content.ModulesContent) > 0 && string(c.Content.ModulesContent) != "null":
		return true, "modulesContent"
	case len(c.Content.ModuleContent) > 0 && string(c.Content.ModuleContent) != "null":
		return false, "moduleContent"
	default:
		return false, "deviceContent"
	}
}

// stringMap converts a Go map to a framework map; empty maps follow prior:
// null when prior is null/unknown, {} when prior is a known map (the hub
// returns null for labels it never had and {} for cleared ones — Terraform
// state must follow the configuration instead).
func stringMap(m map[string]string, prior types.Map) types.Map {
	if len(m) == 0 {
		if prior.IsNull() || prior.IsUnknown() {
			return types.MapNull(types.StringType)
		}
		return types.MapValueMust(types.StringType, map[string]attr.Value{})
	}
	elems := make(map[string]attr.Value, len(m))
	for k, v := range m {
		elems[k] = types.StringValue(v)
	}
	return types.MapValueMust(types.StringType, elems)
}

func int64Map(m map[string]int64) types.Map {
	elems := make(map[string]attr.Value, len(m))
	for k, v := range m {
		elems[k] = types.Int64Value(v)
	}
	return types.MapValueMust(types.Int64Type, elems)
}

// mapToGo converts a framework map(string) to a Go map; null/unknown → nil,
// known empty → empty (non-nil).
func mapToGo(ctx context.Context, m types.Map) (map[string]string, diag.Diagnostics) {
	if m.IsNull() || m.IsUnknown() {
		return nil, nil
	}
	out := map[string]string{}
	diags := m.ElementsAs(ctx, &out, false)
	return out, diags
}

// content keeps a raw section as-is when the owner's document is
// semantically equal (so the configured string is preserved verbatim), and
// re-encodes the hub's value otherwise.
func content(t jsondoc.Type, raw json.RawMessage, owner jsondoc.Value) jsondoc.Value {
	if len(raw) == 0 || string(raw) == "null" {
		return jsondoc.NewNull(t)
	}
	if !owner.IsNull() && !owner.IsUnknown() && jsondoc.SemanticallyEqual(owner.ValueString(), string(raw)) {
		return owner
	}
	if doc, err := twinpatch.Decode(string(raw)); err == nil {
		return jsondoc.NewValue(t, twinpatch.Encode(doc))
	}
	return jsondoc.NewValue(t, string(raw))
}

// fromHub maps a hub configuration onto the model. owner supplies the prior
// (plan or state) values that decide null-vs-empty and verbatim strings.
func (k kind) fromHub(m *model, c *client.Configuration, owner model) {
	m.ID = types.StringValue(c.ID)
	m.ConfigurationID = types.StringValue(c.ID)
	m.TargetCondition = types.StringValue(c.TargetCondition)
	m.Priority = types.Int64Value(c.Priority)
	m.Labels = stringMap(c.Labels, owner.Labels)
	var queries map[string]string
	if c.Metrics != nil {
		queries = c.Metrics.Queries
	}
	m.Metrics = stringMap(queries, owner.Metrics)
	m.SchemaVersion = types.StringNull()
	if c.SchemaVersion != "" {
		m.SchemaVersion = types.StringValue(c.SchemaVersion)
	}
	m.ETag = types.StringValue(c.ETag)
	m.CreatedTime = types.StringValue(c.CreatedTimeUTC)
	m.LastUpdatedTime = types.StringValue(c.LastUpdatedTimeUTC)
	var sys, res map[string]int64
	if c.SystemMetrics != nil {
		sys = c.SystemMetrics.Results
	}
	if c.Metrics != nil {
		res = c.Metrics.Results
	}
	m.SystemMetrics = int64Map(sys)
	m.MetricResults = int64Map(res)
	m.DeviceContent = content(ContentType, c.Content.DeviceContent, owner.DeviceContent)
	m.ModuleContent = content(ContentType, c.Content.ModuleContent, owner.ModuleContent)
	m.ModulesContent = content(ModulesContentType, c.Content.ModulesContent, owner.ModulesContent)
}

// spec renders the model for the wire.
func (k kind) spec(ctx context.Context, m model) (client.ConfigurationSpec, diag.Diagnostics) {
	var diags diag.Diagnostics
	spec := client.ConfigurationSpec{
		ID:              m.ConfigurationID.ValueString(),
		SchemaVersion:   m.SchemaVersion.ValueString(),
		TargetCondition: m.TargetCondition.ValueString(),
		Priority:        m.Priority.ValueInt64(),
	}
	var d diag.Diagnostics
	spec.Labels, d = mapToGo(ctx, m.Labels)
	diags.Append(d...)
	spec.Metrics, d = mapToGo(ctx, m.Metrics)
	diags.Append(d...)
	raw := func(v jsondoc.Value) json.RawMessage {
		if v.IsNull() || v.IsUnknown() {
			return nil
		}
		return json.RawMessage(v.ValueString())
	}
	spec.Content = client.ConfigurationContent{
		DeviceContent:  raw(m.DeviceContent),
		ModuleContent:  raw(m.ModuleContent),
		ModulesContent: raw(m.ModulesContent),
	}
	return spec, diags
}

// ---- conflict inspection ------------------------------------------------------------

// written are the fields the provider writes (CONCEPT.md §11.1).
type written struct {
	Priority        int64
	TargetCondition string
	SchemaVersion   string
	Labels          map[string]string
	Metrics         map[string]string
	Content         string // JSON of the section
}

func writtenFromHub(c *client.Configuration) written {
	w := written{Priority: c.Priority, TargetCondition: c.TargetCondition, SchemaVersion: c.SchemaVersion, Labels: c.Labels}
	if c.Metrics != nil {
		w.Metrics = c.Metrics.Queries
	}
	_, section := contentKind(c)
	switch section {
	case "modulesContent":
		w.Content = string(c.Content.ModulesContent)
	case "moduleContent":
		w.Content = string(c.Content.ModuleContent)
	default:
		w.Content = string(c.Content.DeviceContent)
	}
	return w
}

func writtenFromModel(ctx context.Context, m model) written {
	labels, _ := mapToGo(ctx, m.Labels)
	metrics, _ := mapToGo(ctx, m.Metrics)
	w := written{Priority: m.Priority.ValueInt64(), TargetCondition: m.TargetCondition.ValueString(), SchemaVersion: m.SchemaVersion.ValueString(), Labels: labels, Metrics: metrics}
	for _, v := range []jsondoc.Value{m.DeviceContent, m.ModuleContent, m.ModulesContent} {
		if !v.IsNull() && !v.IsUnknown() {
			w.Content = v.ValueString()
		}
	}
	return w
}

// diffWritten lists the differences between prior and fresh.
func diffWritten(prior, fresh written) []string {
	var out []string
	if prior.Priority != fresh.Priority {
		out = append(out, fmt.Sprintf("priority: %d → %d", prior.Priority, fresh.Priority))
	}
	if prior.TargetCondition != fresh.TargetCondition {
		out = append(out, fmt.Sprintf("target_condition: %q → %q", prior.TargetCondition, fresh.TargetCondition))
	}
	if prior.SchemaVersion != fresh.SchemaVersion {
		out = append(out, fmt.Sprintf("schema_version: %q → %q", prior.SchemaVersion, fresh.SchemaVersion))
	}
	if !equalStringMaps(prior.Labels, fresh.Labels) {
		out = append(out, "labels: "+describeMap(prior.Labels)+" → "+describeMap(fresh.Labels))
	}
	if !equalStringMaps(prior.Metrics, fresh.Metrics) {
		out = append(out, "metrics: "+describeMap(prior.Metrics)+" → "+describeMap(fresh.Metrics))
	}
	if (prior.Content == "") != (fresh.Content == "") || (prior.Content != "" && !jsondoc.SemanticallyEqual(prior.Content, fresh.Content)) {
		out = append(out, "content: (changed)")
	}
	return out
}

func equalStringMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

func describeMap(m map[string]string) string {
	if len(m) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%q", k, m[k]))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}
