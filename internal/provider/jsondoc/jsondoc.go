// Package jsondoc provides a custom string type for attributes that hold a
// JSON object: values compare semantically (key order, whitespace and number
// formatting are irrelevant), are validated as objects at plan time — with an
// optional, type-specific rule set — and come with a plan modifier that
// requires replacement only when the object really changed.
package jsondoc

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/attr/xattr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/mwalser/terraform-provider-iothub/internal/twinpatch"
)

var (
	_ basetypes.StringTypable                    = Type{}
	_ basetypes.StringValuableWithSemanticEquals = Value{}
	_ xattr.ValidateableAttribute                = Value{}
)

// Validate checks a decoded object and returns human-readable problems.
type Validate func(doc map[string]any) []string

// Type is the attribute type. Name identifies the rule set (types with the
// same Name are equal); Validate adds rules beyond "must be a JSON object".
type Type struct {
	basetypes.StringType
	Name     string
	Validate Validate
}

// Equal implements attr.Type.
func (t Type) Equal(o attr.Type) bool {
	ot, ok := o.(Type)
	return ok && ot.Name == t.Name
}

// String implements attr.Type.
func (t Type) String() string { return "jsondoc.Type(" + t.Name + ")" }

// ValueFromString implements basetypes.StringTypable.
func (t Type) ValueFromString(_ context.Context, in basetypes.StringValue) (basetypes.StringValuable, diag.Diagnostics) {
	return Value{StringValue: in, typ: t}, nil
}

// ValueFromTerraform implements attr.Type.
func (t Type) ValueFromTerraform(ctx context.Context, in tftypes.Value) (attr.Value, error) {
	v, err := t.StringType.ValueFromTerraform(ctx, in)
	if err != nil {
		return nil, err
	}
	sv, ok := v.(basetypes.StringValue)
	if !ok {
		return nil, fmt.Errorf("unexpected value type %T", v)
	}
	return Value{StringValue: sv, typ: t}, nil
}

// ValueType implements attr.Type.
func (t Type) ValueType(_ context.Context) attr.Value { return Value{typ: t} }

// Value is a Type value.
type Value struct {
	basetypes.StringValue
	typ Type
}

// NewValue wraps a JSON string.
func NewValue(t Type, s string) Value { return Value{StringValue: basetypes.NewStringValue(s), typ: t} }

// NewNull returns a null value.
func NewNull(t Type) Value { return Value{StringValue: basetypes.NewStringNull(), typ: t} }

// Equal implements attr.Value (exact comparison; see StringSemanticEquals).
func (v Value) Equal(o attr.Value) bool {
	other, ok := o.(Value)
	return ok && v.StringValue.Equal(other.StringValue)
}

// Type implements attr.Value.
func (v Value) Type(_ context.Context) attr.Type { return v.typ }

// StringSemanticEquals treats two values as equal when they decode to equal
// JSON objects (numbers by value).
func (v Value) StringSemanticEquals(_ context.Context, newValuable basetypes.StringValuable) (bool, diag.Diagnostics) {
	var diags diag.Diagnostics
	other, ok := newValuable.(Value)
	if !ok {
		diags.AddError("Semantic equality check error", fmt.Sprintf("Expected a JSON document value, got %T. This is a bug in the provider.", newValuable))
		return false, diags
	}
	return SemanticallyEqual(v.ValueString(), other.ValueString()), diags
}

// SemanticallyEqual reports whether two JSON strings decode to equal objects.
// Undecodable strings are never equal.
func SemanticallyEqual(a, b string) bool {
	da, err := twinpatch.Decode(a)
	if err != nil {
		return false
	}
	db, err := twinpatch.Decode(b)
	if err != nil {
		return false
	}
	return twinpatch.Equal(da, db)
}

// ValidateAttribute rejects anything but a JSON object obeying the type's
// rules.
func (v Value) ValidateAttribute(_ context.Context, req xattr.ValidateAttributeRequest, resp *xattr.ValidateAttributeResponse) {
	if v.IsNull() || v.IsUnknown() {
		return
	}
	doc, err := twinpatch.Decode(v.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid JSON document", err.Error()+". Use jsonencode({...}) to build the value.")
		return
	}
	if v.typ.Validate != nil {
		for _, p := range v.typ.Validate(doc) {
			resp.Diagnostics.AddAttributeError(req.Path, "Invalid JSON document", p)
		}
	}
}

// Object decodes the value; null/unknown yield nil.
func (v Value) Object() (map[string]any, error) {
	if v.IsNull() || v.IsUnknown() {
		return nil, nil
	}
	return twinpatch.Decode(v.ValueString())
}

// ---- plan modifier -----------------------------------------------------------

// RequiresReplaceIfChanged requires replacement when the planned document
// differs semantically from the prior state — a reformatted but equal
// document is an in-place no-op, not a replacement.
func RequiresReplaceIfChanged() planmodifier.String { return requiresReplaceIfChanged{} }

type requiresReplaceIfChanged struct{}

func (requiresReplaceIfChanged) Description(ctx context.Context) string {
	return "Requires replacement when the JSON object changes (semantically)."
}

func (m requiresReplaceIfChanged) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (requiresReplaceIfChanged) PlanModifyString(_ context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() { // create or destroy
		return
	}
	if req.PlanValue.IsUnknown() {
		resp.RequiresReplace = true // cannot tell yet
		return
	}
	if req.PlanValue.IsNull() != req.StateValue.IsNull() {
		resp.RequiresReplace = true
		return
	}
	if req.PlanValue.IsNull() {
		return
	}
	if !SemanticallyEqual(req.PlanValue.ValueString(), req.StateValue.ValueString()) {
		resp.RequiresReplace = true
	}
}
