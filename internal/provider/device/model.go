// Package device implements iothub_device (resource, data source) and the
// iothub_device_credentials ephemeral resource. Behaviour follows CONCEPT.md
// §6.1, §11.1 and §11.3 and the service facts in Appendix D.
package device

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
)

// deviceIDPattern is the identity registry's ID charset: up to 128 ASCII
// alphanumerics plus - : . + % _ # * ? ! ( ) , = @ ; $ '.
var deviceIDPattern = regexp.MustCompile(`^[A-Za-z0-9\-:.+%_#*?!(),=@;$']{1,128}$`)

// thumbprintPattern accepts SHA-1 (40) or SHA-256 (64) hex digests without
// separators; the service rejects separators and preserves case.
var thumbprintPattern = regexp.MustCompile(`^(?:[0-9A-Fa-f]{40}|[0-9A-Fa-f]{64})$`)

// parentScopePrefix is how edge device scopes look (ms-azure-iot-edge://<id>-<gen>).
const parentScopePrefix = "ms-azure-iot-edge://"

// authModel is the nested `authentication` attribute.
type authModel struct {
	Type                types.String `tfsdk:"type"`
	PrimaryKey          types.String `tfsdk:"primary_key"`
	SecondaryKey        types.String `tfsdk:"secondary_key"`
	PrimaryThumbprint   types.String `tfsdk:"primary_thumbprint"`
	SecondaryThumbprint types.String `tfsdk:"secondary_thumbprint"`
}

var authAttrTypes = map[string]attr.Type{
	"type":                 types.StringType,
	"primary_key":          types.StringType,
	"secondary_key":        types.StringType,
	"primary_thumbprint":   types.StringType,
	"secondary_thumbprint": types.StringType,
}

func (a authModel) object() types.Object {
	return types.ObjectValueMust(authAttrTypes, map[string]attr.Value{
		"type":                 a.Type,
		"primary_key":          a.PrimaryKey,
		"secondary_key":        a.SecondaryKey,
		"primary_thumbprint":   a.PrimaryThumbprint,
		"secondary_thumbprint": a.SecondaryThumbprint,
	})
}

// authFromObject decodes the nested attribute; a null/unknown object yields
// ok=false.
func authFromObject(ctx context.Context, o types.Object) (authModel, bool, diag.Diagnostics) {
	var a authModel
	if o.IsNull() || o.IsUnknown() {
		return a, false, nil
	}
	diags := o.As(ctx, &a, basetypesObjectAsOptions)
	return a, !diags.HasError(), diags
}

// stringOrNull maps "" to null so hub nulls and empty strings compare equal.
func stringOrNull(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}

// authFromHub maps the service's authentication mechanism to the nested
// attribute. keysInState=false (write-only keys in use) leaves the key
// attributes null so the secrets never enter state (CONCEPT.md §11.3).
func authFromHub(am *client.AuthenticationMechanism, keysInState bool) authModel {
	a := authModel{
		Type:                types.StringNull(),
		PrimaryKey:          types.StringNull(),
		SecondaryKey:        types.StringNull(),
		PrimaryThumbprint:   types.StringNull(),
		SecondaryThumbprint: types.StringNull(),
	}
	if am == nil {
		return a
	}
	a.Type = stringOrNull(am.Type)
	if am.SymmetricKey != nil && keysInState {
		a.PrimaryKey = stringOrNull(am.SymmetricKey.PrimaryKey)
		a.SecondaryKey = stringOrNull(am.SymmetricKey.SecondaryKey)
	}
	if am.X509Thumbprint != nil {
		a.PrimaryThumbprint = stringOrNull(am.X509Thumbprint.PrimaryThumbprint)
		a.SecondaryThumbprint = stringOrNull(am.X509Thumbprint.SecondaryThumbprint)
	}
	return a
}

// parentScopeOf derives the single parent relationship the resource exposes:
// edge devices carry it in parentScopes, leaf devices in deviceScope.
func parentScopeOf(d *client.Device) string {
	if d.Capabilities != nil && d.Capabilities.IotEdge {
		if len(d.ParentScopes) > 0 {
			return d.ParentScopes[0]
		}
		return ""
	}
	return d.DeviceScope
}

// writtenFields are the identity fields the provider writes, i.e. the ones
// conflict inspection compares (CONCEPT.md §11.1). Volatile fields
// (connection state, activity time, message count) are deliberately absent.
type writtenFields struct {
	Status, StatusReason string
	IotEdge              bool
	ParentScope          string
	AuthType             string
	PrimaryKey           string // "" when unknown to the comparer
	SecondaryKey         string
	PrimaryThumbprint    string
	SecondaryThumbprint  string
}

func writtenFromHub(d *client.Device) writtenFields {
	w := writtenFields{Status: d.Status, IotEdge: d.Capabilities != nil && d.Capabilities.IotEdge, ParentScope: parentScopeOf(d)}
	if d.StatusReason != nil {
		w.StatusReason = *d.StatusReason
	}
	if d.Authentication != nil {
		w.AuthType = d.Authentication.Type
		if d.Authentication.SymmetricKey != nil {
			w.PrimaryKey, w.SecondaryKey = d.Authentication.SymmetricKey.PrimaryKey, d.Authentication.SymmetricKey.SecondaryKey
		}
		if d.Authentication.X509Thumbprint != nil {
			w.PrimaryThumbprint, w.SecondaryThumbprint = d.Authentication.X509Thumbprint.PrimaryThumbprint, d.Authentication.X509Thumbprint.SecondaryThumbprint
		}
	}
	return w
}

// diffWritten lists the written fields that differ between what the plan
// was built from (prior) and what the hub holds now (fresh). Keys are only
// compared when prior knows them (they are absent from state with
// write-only keys). Thumbprints compare case-insensitively.
func diffWritten(prior, fresh writtenFields) []string {
	var out []string
	add := func(name, a, b string) {
		if a != b {
			out = append(out, fmt.Sprintf("%s: %q → %q", name, a, b))
		}
	}
	add("status", prior.Status, fresh.Status)
	add("status_reason", prior.StatusReason, fresh.StatusReason)
	if prior.IotEdge != fresh.IotEdge {
		out = append(out, fmt.Sprintf("edge_enabled: %v → %v", prior.IotEdge, fresh.IotEdge))
	}
	add("parent_scope", prior.ParentScope, fresh.ParentScope)
	add("authentication.type", prior.AuthType, fresh.AuthType)
	if prior.PrimaryKey != "" && prior.PrimaryKey != fresh.PrimaryKey {
		out = append(out, "authentication.primary_key: (rotated)")
	}
	if prior.SecondaryKey != "" && prior.SecondaryKey != fresh.SecondaryKey {
		out = append(out, "authentication.secondary_key: (rotated)")
	}
	if !strings.EqualFold(prior.PrimaryThumbprint, fresh.PrimaryThumbprint) {
		add("authentication.primary_thumbprint", prior.PrimaryThumbprint, fresh.PrimaryThumbprint)
	}
	if !strings.EqualFold(prior.SecondaryThumbprint, fresh.SecondaryThumbprint) {
		add("authentication.secondary_thumbprint", prior.SecondaryThumbprint, fresh.SecondaryThumbprint)
	}
	return out
}
