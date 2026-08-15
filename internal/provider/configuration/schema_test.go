package configuration

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr/xattr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/jsondoc"
)

func TestSchemas_ValidateImplementation(t *testing.T) {
	ctx := context.Background()
	for name, r := range map[string]resource.Resource{"configuration": NewConfigurationResource(), "edge": NewEdgeDeploymentResource()} {
		var rs resource.SchemaResponse
		r.Schema(ctx, resource.SchemaRequest{}, &rs)
		if diags := rs.Schema.ValidateImplementation(ctx); diags.HasError() {
			t.Fatalf("%s resource schema: %v", name, diags)
		}
		_, edge := rs.Schema.Attributes["modules_content"]
		_, dev := rs.Schema.Attributes["device_content"]
		if edge != (name == "edge") || dev == (name == "edge") {
			t.Errorf("%s: content attributes wrong (modules_content=%v device_content=%v)", name, edge, dev)
		}
		for _, a := range []string{"id", "hostname", "target_condition", "priority", "labels", "metrics", "schema_version", "etag", "system_metrics", "metric_results"} {
			if _, ok := rs.Schema.Attributes[a]; !ok {
				t.Errorf("%s: missing %q", name, a)
			}
		}
	}
	for name, d := range map[string]datasource.DataSource{"configuration": NewConfigurationDataSource(), "edge": NewEdgeDeploymentDataSource()} {
		var ds datasource.SchemaResponse
		d.Schema(ctx, datasource.SchemaRequest{}, &ds)
		if diags := ds.Schema.ValidateImplementation(ctx); diags.HasError() {
			t.Fatalf("%s data source schema: %v", name, diags)
		}
	}
}

func TestContentTypes(t *testing.T) {
	ctx := context.Background()
	check := func(typ jsondoc.Type, s string) bool {
		var resp xattr.ValidateAttributeResponse
		jsondoc.NewValue(typ, s).ValidateAttribute(ctx, xattr.ValidateAttributeRequest{Path: path.Root("x")}, &resp)
		return resp.Diagnostics.HasError()
	}
	if check(ContentType, `{"properties.desired.firmware":{"channel":"stable"},"properties.desired":{"a":1}}`) {
		t.Error("valid device content rejected")
	}
	if !check(ContentType, `{"tags.site":"x"}`) || !check(ContentType, `{"properties.reported.x":1}`) || !check(ContentType, `[]`) {
		t.Error("invalid device content accepted")
	}
	if check(ModulesContentType, `{"$edgeAgent":{"properties.desired":{"schemaVersion":"1.1"}},"$edgeHub":{"properties.desired.routes.r":"FROM /* INTO $upstream"}}`) {
		t.Error("valid modules content rejected")
	}
	if check(ModulesContentType, `{"$edgeAgent":{"properties.desired.modules.x":{"type":"docker"}}}`) {
		t.Error("layered content rejected")
	}
	if !check(ModulesContentType, `{"$edgeHub":{"properties.desired":{}}}`) {
		t.Error("modules content without $edgeAgent accepted")
	}
	if !check(ModulesContentType, `{"$edgeAgent":"nope"}`) || !check(ModulesContentType, `{"$edgeAgent":{"tags.x":1}}`) {
		t.Error("bad module entries accepted")
	}
}

func TestIDPatternAndDiff(t *testing.T) {
	for _, ok := range []string{"a", "fw-channel+1%_*!'", "0123"} {
		if !idPattern.MatchString(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "Upper", "a.b", "a:b", "a b", "a(b)", "a,b", "a=b", "a@b", "a;b", "a$b"} {
		if idPattern.MatchString(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
	prior := written{Priority: 1, TargetCondition: "*", Labels: map[string]string{"a": "b"}, Content: `{"properties.desired.x":1}`}
	if d := diffWritten(prior, prior); len(d) != 0 {
		t.Errorf("identical: %v", d)
	}
	fresh := prior
	fresh.Content = `{ "properties.desired.x" : 1.0 }`
	if d := diffWritten(prior, fresh); len(d) != 0 {
		t.Errorf("semantically equal content must not diff: %v", d)
	}
	fresh.Priority, fresh.Labels, fresh.Metrics = 2, map[string]string{"a": "c"}, map[string]string{"m": "SELECT 1"}
	fresh.Content = `{"properties.desired.x":2}`
	d := diffWritten(prior, fresh)
	if len(d) != 4 || d[0] != "priority: 1 → 2" || d[1] != `labels: {a="b"} → {a="c"}` || d[2] != `metrics: {} → {m="SELECT 1"}` || d[3] != "content: (changed)" {
		t.Errorf("diff: %v", d)
	}
	edge, section := contentKind(&client.Configuration{Content: client.ConfigurationContent{ModulesContent: []byte(`{"$edgeAgent":{}}`)}})
	if !edge || section != "modulesContent" {
		t.Error("contentKind edge")
	}
	if edge, section = contentKind(&client.Configuration{Content: client.ConfigurationContent{ModuleContent: []byte(`{}`)}}); edge || section != "moduleContent" {
		t.Error("contentKind module")
	}
}
