// Package twin implements iothub_device_twin and iothub_module_twin
// (resources and data sources): partial, leaf-path ownership of twin tags
// and desired properties as specified in CONCEPT.md §6.3, on top of the
// twinpatch engine.
package twin

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/attr/xattr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/mwalser/terraform-provider-iothub/internal/twinpatch"
)

var (
	_ basetypes.StringTypable                    = DocumentType{}
	_ basetypes.StringValuableWithSemanticEquals = Document{}
	_ xattr.ValidateableAttribute                = Document{}
)

// DocumentType is the attribute type of twin sections (`tags`,
// `desired_properties`): a JSON object as a string, compared semantically
// (key order, whitespace and number formatting do not matter) and validated
// against the service's twin rules.
type DocumentType struct {
	basetypes.StringType
}

// Equal implements attr.Type.
func (t DocumentType) Equal(o attr.Type) bool {
	_, ok := o.(DocumentType)
	return ok
}

// String implements attr.Type.
func (t DocumentType) String() string { return "twin.DocumentType" }

// ValueFromString implements basetypes.StringTypable.
func (t DocumentType) ValueFromString(_ context.Context, in basetypes.StringValue) (basetypes.StringValuable, diag.Diagnostics) {
	return Document{StringValue: in}, nil
}

// ValueFromTerraform implements attr.Type.
func (t DocumentType) ValueFromTerraform(ctx context.Context, in tftypes.Value) (attr.Value, error) {
	v, err := t.StringType.ValueFromTerraform(ctx, in)
	if err != nil {
		return nil, err
	}
	sv, ok := v.(basetypes.StringValue)
	if !ok {
		return nil, fmt.Errorf("unexpected value type %T", v)
	}
	return Document{StringValue: sv}, nil
}

// ValueType implements attr.Type.
func (t DocumentType) ValueType(_ context.Context) attr.Value { return Document{} }

// Document is a DocumentType value.
type Document struct {
	basetypes.StringValue
}

// NewDocumentValue wraps a JSON string.
func NewDocumentValue(s string) Document { return Document{StringValue: basetypes.NewStringValue(s)} }

// NewDocumentNull returns a null document.
func NewDocumentNull() Document { return Document{StringValue: basetypes.NewStringNull()} }

// Equal implements attr.Value (exact comparison; see StringSemanticEquals).
func (v Document) Equal(o attr.Value) bool {
	other, ok := o.(Document)
	return ok && v.StringValue.Equal(other.StringValue)
}

// Type implements attr.Value.
func (v Document) Type(_ context.Context) attr.Type { return DocumentType{} }

// StringSemanticEquals treats two documents as equal when they decode to
// equal JSON objects (twinpatch.Equal: numbers by value).
func (v Document) StringSemanticEquals(_ context.Context, newValuable basetypes.StringValuable) (bool, diag.Diagnostics) {
	var diags diag.Diagnostics
	other, ok := newValuable.(Document)
	if !ok {
		diags.AddError("Semantic equality check error", fmt.Sprintf("Expected a twin document, got %T. This is a bug in the provider.", newValuable))
		return false, diags
	}
	a, err := twinpatch.Decode(v.ValueString())
	if err != nil {
		return false, diags
	}
	b, err := twinpatch.Decode(other.ValueString())
	if err != nil {
		return false, diags
	}
	return twinpatch.Equal(a, b), diags
}

// ValidateAttribute rejects anything but a JSON object that obeys the twin
// rules (key charset and size, value size, depth, no nulls).
func (v Document) ValidateAttribute(_ context.Context, req xattr.ValidateAttributeRequest, resp *xattr.ValidateAttributeResponse) {
	if v.IsNull() || v.IsUnknown() {
		return
	}
	doc, err := twinpatch.Decode(v.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid twin document", err.Error()+". Use jsonencode({...}) to build the value.")
		return
	}
	for _, p := range twinpatch.Validate(doc) {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid twin document", p.String())
	}
}

// Object decodes the document; null/unknown yield nil.
func (v Document) Object() (map[string]any, error) {
	if v.IsNull() || v.IsUnknown() {
		return nil, nil
	}
	return twinpatch.Decode(v.ValueString())
}
