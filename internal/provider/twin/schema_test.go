package twin

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr/xattr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

func TestSchemas_ValidateImplementation(t *testing.T) {
	ctx := context.Background()
	for name, r := range map[string]resource.Resource{"device": NewDeviceResource(), "module": NewModuleResource()} {
		var rs resource.SchemaResponse
		r.Schema(ctx, resource.SchemaRequest{}, &rs)
		if diags := rs.Schema.ValidateImplementation(ctx); diags.HasError() {
			t.Fatalf("%s resource schema: %v", name, diags)
		}
		_, hasModule := rs.Schema.Attributes["module_id"]
		if hasModule != (name == "module") {
			t.Errorf("%s: module_id presence = %v", name, hasModule)
		}
		for _, a := range []string{"id", "hostname", "device_id", "tags", "desired_properties", "etag", "version"} {
			if _, ok := rs.Schema.Attributes[a]; !ok {
				t.Errorf("%s: missing %q", name, a)
			}
		}
	}
	for name, d := range map[string]datasource.DataSource{"device": NewDeviceDataSource(), "module": NewModuleDataSource()} {
		var ds datasource.SchemaResponse
		d.Schema(ctx, datasource.SchemaRequest{}, &ds)
		if diags := ds.Schema.ValidateImplementation(ctx); diags.HasError() {
			t.Fatalf("%s data source schema: %v", name, diags)
		}
		if _, ok := ds.Schema.Attributes["reported_properties"]; !ok {
			t.Errorf("%s: missing reported_properties", name)
		}
	}
}

func TestDocument(t *testing.T) {
	ctx := context.Background()
	a := NewDocumentValue(`{"b":1,"a":{"x":[1,2]}}`)
	b := NewDocumentValue(` {"a": {"x": [1.0, 2]}, "b": 1.0} `)
	if eq, _ := a.StringSemanticEquals(ctx, b); !eq {
		t.Error("semantically equal documents")
	}
	if eq, _ := a.StringSemanticEquals(ctx, NewDocumentValue(`{"b":2}`)); eq {
		t.Error("different documents")
	}
	if a.Equal(b) {
		t.Error("Equal is exact")
	}
	// validation
	for s, wantErr := range map[string]bool{
		`{"a":1}`:        false,
		`{}`:             false,
		`[1]`:            true,
		`not json`:       true,
		`{"a.b":1}`:      true,
		`{"a":null}`:     true,
		`{"a":{"b":{}}}`: false,
	} {
		var resp xattr.ValidateAttributeResponse
		NewDocumentValue(s).ValidateAttribute(ctx, xattr.ValidateAttributeRequest{Path: path.Root("tags")}, &resp)
		if resp.Diagnostics.HasError() != wantErr {
			t.Errorf("%s: error=%v, want %v (%v)", s, resp.Diagnostics.HasError(), wantErr, resp.Diagnostics)
		}
	}
	// nulls
	var resp xattr.ValidateAttributeResponse
	NewDocumentNull().ValidateAttribute(ctx, xattr.ValidateAttributeRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Error("null must validate")
	}
	if v, err := NewDocumentNull().Object(); v != nil || err != nil {
		t.Error("null object")
	}
	// type plumbing
	var typ DocumentType
	v, diags := typ.ValueFromString(ctx, basetypes.NewStringValue(`{}`))
	if diags.HasError() || !v.Equal(NewDocumentValue(`{}`)) {
		t.Error("ValueFromString")
	}
	if !typ.Equal(DocumentType{}) || typ.Equal(basetypes.StringType{}) {
		t.Error("type equality")
	}
}
