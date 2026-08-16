// Package identity holds what the identity-registry resources (iothub_device,
// iothub_module) share: the ID rules and the `authentication` block — its
// schema, validation, planning, the wire representation and the conflict
// inspection of CONCEPT.md §11.1/§11.3.
package identity

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/common"
)

// IDPattern is the identity registry's ID charset for devices and modules:
// up to 128 ASCII alphanumerics plus - : . + % _ # * ? ! ( ) , = @ ; $ '.
var IDPattern = regexp.MustCompile(`^[A-Za-z0-9\-:.+%_#*?!(),=@;$']{1,128}$`)

// IDDescription documents IDPattern.
const IDDescription = "1–128 ASCII characters from `A-Z a-z 0-9 - : . + % _ # * ? ! ( ) , = @ ; $ '`"

// IDValidator validates a device or module ID.
func IDValidator() validator.String {
	return stringvalidator.RegexMatches(IDPattern, "must be "+IDDescription)
}

// ThumbprintPattern accepts SHA-1 (40) or SHA-256 (64) hex digests without
// separators; the service rejects separators and preserves case.
var ThumbprintPattern = regexp.MustCompile(`^(?:[0-9A-Fa-f]{40}|[0-9A-Fa-f]{64})$`)

// objectAsOptions tolerates null/unknown nested values when decoding.
var objectAsOptions = basetypes.ObjectAsOptions{UnhandledNullAsEmpty: true, UnhandledUnknownAsEmpty: true}

// Auth is the nested `authentication` attribute.
type Auth struct {
	Type                types.String `tfsdk:"type"`
	PrimaryKey          types.String `tfsdk:"primary_key"`
	SecondaryKey        types.String `tfsdk:"secondary_key"`
	PrimaryThumbprint   types.String `tfsdk:"primary_thumbprint"`
	SecondaryThumbprint types.String `tfsdk:"secondary_thumbprint"`
}

// AuthAttrTypes is the object type of the `authentication` attribute.
var AuthAttrTypes = map[string]attr.Type{
	"type":                 types.StringType,
	"primary_key":          types.StringType,
	"secondary_key":        types.StringType,
	"primary_thumbprint":   types.StringType,
	"secondary_thumbprint": types.StringType,
}

// Object renders the block as a framework object.
func (a Auth) Object() types.Object {
	return types.ObjectValueMust(AuthAttrTypes, map[string]attr.Value{
		"type":                 a.Type,
		"primary_key":          a.PrimaryKey,
		"secondary_key":        a.SecondaryKey,
		"primary_thumbprint":   a.PrimaryThumbprint,
		"secondary_thumbprint": a.SecondaryThumbprint,
	})
}

// AuthFromObject decodes the nested attribute; a null/unknown object yields
// ok=false.
func AuthFromObject(ctx context.Context, o types.Object) (Auth, bool, diag.Diagnostics) {
	var a Auth
	if o.IsNull() || o.IsUnknown() {
		return a, false, nil
	}
	diags := o.As(ctx, &a, objectAsOptions)
	return a, !diags.HasError(), diags
}

// StringOrNull maps "" to null so hub nulls and empty strings compare equal.
func StringOrNull(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}

// TimeOrNull is StringOrNull for the service's timestamps, rendered
// canonically (RFC 3339 UTC, fractional seconds without trailing zeros). The
// service itself is inconsistent — a PUT/POST answer says
// `0001-01-01T00:00:00Z`, a GET or the twin `0001-01-01T00:00:00.0000000Z`
// for the same instant — and since a refresh may take either path, state
// must not depend on which endpoint answered last. Unparseable values are
// kept verbatim.
func TimeOrNull(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return types.StringValue(s)
	}
	return types.StringValue(t.UTC().Format(time.RFC3339Nano))
}

// AuthFromHub maps the service's authentication mechanism to the nested
// attribute. keysInState=false (write-only keys in use) leaves the key
// attributes null so the secrets never enter state (CONCEPT.md §11.3).
func AuthFromHub(am *client.AuthenticationMechanism, keysInState bool) Auth {
	a := Auth{
		Type:                types.StringNull(),
		PrimaryKey:          types.StringNull(),
		SecondaryKey:        types.StringNull(),
		PrimaryThumbprint:   types.StringNull(),
		SecondaryThumbprint: types.StringNull(),
	}
	if am == nil {
		return a
	}
	a.Type = StringOrNull(am.Type)
	if am.SymmetricKey != nil && keysInState {
		a.PrimaryKey = StringOrNull(am.SymmetricKey.PrimaryKey)
		a.SecondaryKey = StringOrNull(am.SymmetricKey.SecondaryKey)
	}
	if am.X509Thumbprint != nil {
		a.PrimaryThumbprint = StringOrNull(am.X509Thumbprint.PrimaryThumbprint)
		a.SecondaryThumbprint = StringOrNull(am.X509Thumbprint.SecondaryThumbprint)
	}
	return a
}

// ---- schema ----------------------------------------------------------------

// AuthAttribute is the `authentication` nested attribute; subject ("device",
// "module") is used in descriptions.
func AuthAttribute(subject string) schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		MarkdownDescription: "How the " + subject + " authenticates. When omitted, the hub generates SAS keys. After an import, " +
			"omitting it keeps the " + subject + "'s existing credentials.",
		Optional: true,
		Computed: true,
		Attributes: map[string]schema.Attribute{
			"type": schema.StringAttribute{
				MarkdownDescription: "`sas` for symmetric keys, `selfSigned` for X.509 thumbprints (`primary_thumbprint` required), or `certificateAuthority` for CA-signed X.509 certificates (neither keys nor thumbprints).",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(client.AuthTypeSAS),
				Validators:          []validator.String{stringvalidator.OneOf(client.AuthTypeSAS, client.AuthTypeSelfSigned, client.AuthTypeCertificateAuthority)},
			},
			"primary_key": schema.StringAttribute{
				MarkdownDescription: "Primary key, base64 encoded (16 to 64 bytes). Generated by the hub when omitted. A key rotated " +
					"outside Terraform is then adopted on refresh. Null when `primary_key_wo` is used.",
				Optional:  true,
				Computed:  true,
				Sensitive: true,
			},
			"secondary_key": schema.StringAttribute{
				MarkdownDescription: "Secondary key, base64 encoded (16 to 64 bytes). Generated by the hub when omitted. A key rotated " +
					"outside Terraform is then adopted on refresh. Null when `secondary_key_wo` is used.",
				Optional:  true,
				Computed:  true,
				Sensitive: true,
			},
			"primary_thumbprint": schema.StringAttribute{
				MarkdownDescription: "Primary X.509 thumbprint for `selfSigned`: 40 or 64 hex characters without separators.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.RegexMatches(ThumbprintPattern, "must be 40 or 64 hex characters without separators")},
			},
			"secondary_thumbprint": schema.StringAttribute{
				MarkdownDescription: "Secondary X.509 thumbprint for `selfSigned`: 40 or 64 hex characters without separators.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.RegexMatches(ThumbprintPattern, "must be 40 or 64 hex characters without separators")},
			},
		},
	}
}

// WriteOnlyKeyAttributes are the top-level write-only key arguments and their
// version markers. (Write-only attributes cannot live inside a computed
// nested attribute, hence top-level.)
func WriteOnlyKeyAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"primary_key_wo": schema.StringAttribute{
			MarkdownDescription: "Write-only primary key, base64 encoded (16 to 64 bytes). Requires `primary_key_wo_version` and " +
				"`secondary_key_wo`. Cannot be combined with `authentication.primary_key`.",
			Optional:  true,
			WriteOnly: true,
			Sensitive: true,
			Validators: []validator.String{
				stringvalidator.AlsoRequires(path.MatchRoot("primary_key_wo_version"), path.MatchRoot("secondary_key_wo")),
				stringvalidator.ConflictsWith(path.MatchRoot("authentication").AtName("primary_key")),
			},
		},
		"primary_key_wo_version": schema.Int64Attribute{
			MarkdownDescription: "Version marker for `primary_key_wo`. Change it to rotate the key.",
			Optional:            true,
			Validators:          []validator.Int64{int64validator.AlsoRequires(path.MatchRoot("primary_key_wo"))},
		},
		"secondary_key_wo": schema.StringAttribute{
			MarkdownDescription: "Write-only secondary key, base64 encoded (16 to 64 bytes). Requires `secondary_key_wo_version` and `primary_key_wo`. Cannot be combined with `authentication.secondary_key`.",
			Optional:            true,
			WriteOnly:           true,
			Sensitive:           true,
			Validators: []validator.String{
				stringvalidator.AlsoRequires(path.MatchRoot("secondary_key_wo_version"), path.MatchRoot("primary_key_wo")),
				stringvalidator.ConflictsWith(path.MatchRoot("authentication").AtName("secondary_key")),
			},
		},
		"secondary_key_wo_version": schema.Int64Attribute{
			MarkdownDescription: "Version marker for `secondary_key_wo`. Change it to rotate the key.",
			Optional:            true,
			Validators:          []validator.Int64{int64validator.AlsoRequires(path.MatchRoot("secondary_key_wo"))},
		},
	}
}

// WriteOnlyKeys carries the write-only key arguments as read from config
// (values) and plan/state (versions).
type WriteOnlyKeys struct {
	Primary          types.String
	PrimaryVersion   types.Int64
	Secondary        types.String
	SecondaryVersion types.Int64
}

// KeysInState reports whether hub-generated/plain keys are stored in state
// (true) or write-only arguments are in use for the respective slot (false).
func (w WriteOnlyKeys) KeysInState() (primary, secondary bool) {
	return w.PrimaryVersion.IsNull(), w.SecondaryVersion.IsNull()
}

// ForUpdate returns the write-only keys to send on an update: a slot's value
// goes out only when its version marker differs from prior (the state), or
// the slot was not write-only before. Write-only values are re-supplied on
// every apply and ephemeral sources such as random_bytes are fresh each run,
// so sending them unconditionally would rotate the keys on any unrelated
// change; the version marker is the contract.
func (w WriteOnlyKeys) ForUpdate(prior WriteOnlyKeys) WriteOnlyKeys {
	out := w
	if sameVersion(w.PrimaryVersion, prior.PrimaryVersion) {
		out.Primary = types.StringNull()
	}
	if sameVersion(w.SecondaryVersion, prior.SecondaryVersion) {
		out.Secondary = types.StringNull()
	}
	return out
}

func sameVersion(a, b types.Int64) bool {
	return !a.IsNull() && !a.IsUnknown() && !b.IsNull() && !b.IsUnknown() && a.ValueInt64() == b.ValueInt64()
}

// ---- validate / plan / write ------------------------------------------------

func known(s types.String) bool { return !s.IsNull() && !s.IsUnknown() }

// ValidateAuth enforces the cross-attribute rules of the authentication
// block: keys only with sas, thumbprints only with selfSigned (which needs at
// least the primary), nothing for certificateAuthority.
func ValidateAuth(ctx context.Context, cfg types.Object, wo WriteOnlyKeys) diag.Diagnostics {
	auth, ok, diags := AuthFromObject(ctx, cfg)
	if diags.HasError() || !ok || auth.Type.IsUnknown() {
		return diags
	}
	authType := client.AuthTypeSAS
	if !auth.Type.IsNull() {
		authType = auth.Type.ValueString()
	}
	hasKeys := known(auth.PrimaryKey) || known(auth.SecondaryKey) || known(wo.Primary) || known(wo.Secondary)
	hasThumbs := known(auth.PrimaryThumbprint) || known(auth.SecondaryThumbprint)
	p := path.Root("authentication")
	switch authType {
	case client.AuthTypeSAS:
		if hasThumbs {
			diags.AddAttributeError(p.AtName("primary_thumbprint"), "Thumbprints need selfSigned authentication", "Set `authentication.type = \"selfSigned\"` or remove the thumbprints.")
		}
	case client.AuthTypeSelfSigned:
		if hasKeys {
			diags.AddAttributeError(p.AtName("primary_key"), "Symmetric keys need sas authentication", "selfSigned identities authenticate with X.509 thumbprints; remove the keys.")
		}
		if auth.PrimaryThumbprint.IsNull() {
			diags.AddAttributeError(p.AtName("primary_thumbprint"), "selfSigned authentication needs a thumbprint", "Set `authentication.primary_thumbprint` (the hub would accept a selfSigned identity without one, but it could never connect).")
		}
	case client.AuthTypeCertificateAuthority:
		if hasKeys || hasThumbs {
			diags.AddAttributeError(p.AtName("type"), "certificateAuthority takes no keys or thumbprints", "The identity authenticates with a CA-signed certificate; remove keys and thumbprints.")
		}
	}
	return diags
}

// PlanAuth decides the planned `authentication` object:
//   - config omitted: keep what the hub holds (state), unknown on create;
//   - sas: thumbprints null; keys from config, else from state when the type
//     was sas before, else unknown (hub-generated or write-only);
//   - selfSigned / certificateAuthority: keys null.
//
// Keys managed through write-only arguments are always planned null. state
// is the prior state's object (null when creating).
func PlanAuth(ctx context.Context, cfg, state types.Object, wo WriteOnlyKeys) (types.Object, diag.Diagnostics) {
	c, cfgSet, diags := AuthFromObject(ctx, cfg)
	st, stSet, d := AuthFromObject(ctx, state)
	diags.Append(d...)
	if diags.HasError() {
		return types.ObjectUnknown(AuthAttrTypes), diags
	}
	if !cfgSet {
		if stSet {
			return st.Object(), diags
		}
		return types.ObjectUnknown(AuthAttrTypes), diags
	}

	authType := client.AuthTypeSAS
	if known(c.Type) {
		authType = c.Type.ValueString()
	}
	out := Auth{
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
		out.PrimaryKey = pick(c.PrimaryKey, wo.PrimaryVersion, st.PrimaryKey)
		out.SecondaryKey = pick(c.SecondaryKey, wo.SecondaryVersion, st.SecondaryKey)
	case client.AuthTypeSelfSigned:
		out.PrimaryThumbprint = c.PrimaryThumbprint
		out.SecondaryThumbprint = c.SecondaryThumbprint
	}
	return out.Object(), diags
}

// BuildAuth renders the authentication mechanism to write from the planned
// block, the write-only config values and (on update) the hub's current
// mechanism, which supplies keys Terraform does not manage.
func BuildAuth(ctx context.Context, planned types.Object, wo WriteOnlyKeys, current *client.AuthenticationMechanism) (client.AuthenticationMechanism, diag.Diagnostics) {
	auth, ok, diags := AuthFromObject(ctx, planned)
	authType := client.AuthTypeSAS
	if ok && known(auth.Type) {
		authType = auth.Type.ValueString()
	} else if !ok && current != nil {
		// authentication omitted from config on an existing identity: keep it.
		return *current, diags
	}
	out := client.AuthenticationMechanism{Type: authType}
	switch authType {
	case client.AuthTypeSAS:
		var cur client.SymmetricKey
		if current != nil && current.SymmetricKey != nil && current.Type == client.AuthTypeSAS {
			cur = *current.SymmetricKey
		}
		primary := ChooseKey(auth.PrimaryKey, wo.Primary, cur.PrimaryKey)
		secondary := ChooseKey(auth.SecondaryKey, wo.Secondary, cur.SecondaryKey)
		// The service wants both keys or neither. Fill a missing counterpart
		// with a fresh random key rather than failing (a create with only
		// one key given, or a slot the hub never had).
		if (primary == "") != (secondary == "") {
			gen, err := common.NewSymmetricKey()
			if err != nil {
				diags.AddError("Cannot generate symmetric key", err.Error())
				return out, diags
			}
			if primary == "" {
				primary = gen
			} else {
				secondary = gen
			}
		}
		if primary != "" {
			out.SymmetricKey = &client.SymmetricKey{PrimaryKey: primary, SecondaryKey: secondary}
		}
	case client.AuthTypeSelfSigned:
		out.X509Thumbprint = &client.X509Thumbprint{
			PrimaryThumbprint:   auth.PrimaryThumbprint.ValueString(),
			SecondaryThumbprint: auth.SecondaryThumbprint.ValueString(),
		}
	}
	return out, diags
}

// ChooseKey picks the key to send: explicit config, else write-only config,
// else what the hub currently holds ("" lets the hub generate on create).
func ChooseKey(configured, writeOnly types.String, current string) string {
	if known(configured) {
		return configured.ValueString()
	}
	if known(writeOnly) {
		return writeOnly.ValueString()
	}
	return current
}

// ---- conflict inspection ---------------------------------------------------

// WrittenAuth are the authentication fields the provider writes, i.e. what
// conflict inspection compares (CONCEPT.md §11.1).
type WrittenAuth struct {
	Type                string
	PrimaryKey          string // "" when unknown to the comparer (write-only)
	SecondaryKey        string
	PrimaryThumbprint   string
	SecondaryThumbprint string
}

// WrittenAuthFromHub reflects the hub's current mechanism.
func WrittenAuthFromHub(am *client.AuthenticationMechanism) WrittenAuth {
	var w WrittenAuth
	if am == nil {
		return w
	}
	w.Type = am.Type
	if am.SymmetricKey != nil {
		w.PrimaryKey, w.SecondaryKey = am.SymmetricKey.PrimaryKey, am.SymmetricKey.SecondaryKey
	}
	if am.X509Thumbprint != nil {
		w.PrimaryThumbprint, w.SecondaryThumbprint = am.X509Thumbprint.PrimaryThumbprint, am.X509Thumbprint.SecondaryThumbprint
	}
	return w
}

// WrittenAuthFromState reflects the prior state's block.
func WrittenAuthFromState(a Auth) WrittenAuth {
	return WrittenAuth{
		Type:                a.Type.ValueString(),
		PrimaryKey:          a.PrimaryKey.ValueString(),
		SecondaryKey:        a.SecondaryKey.ValueString(),
		PrimaryThumbprint:   a.PrimaryThumbprint.ValueString(),
		SecondaryThumbprint: a.SecondaryThumbprint.ValueString(),
	}
}

// DiffAuth lists the authentication fields that differ between what the plan
// was built from (prior) and what the hub holds now (fresh). Keys are only
// compared when prior knows them (they are absent from state with write-only
// keys) and reported without their values. Thumbprints compare
// case-insensitively.
func DiffAuth(prior, fresh WrittenAuth) []string {
	var out []string
	if prior.Type != fresh.Type {
		out = append(out, fmt.Sprintf("authentication.type: %q → %q", prior.Type, fresh.Type))
	}
	if prior.PrimaryKey != "" && prior.PrimaryKey != fresh.PrimaryKey {
		out = append(out, "authentication.primary_key: (rotated)")
	}
	if prior.SecondaryKey != "" && prior.SecondaryKey != fresh.SecondaryKey {
		out = append(out, "authentication.secondary_key: (rotated)")
	}
	if !strings.EqualFold(prior.PrimaryThumbprint, fresh.PrimaryThumbprint) {
		out = append(out, fmt.Sprintf("authentication.primary_thumbprint: %q → %q", prior.PrimaryThumbprint, fresh.PrimaryThumbprint))
	}
	if !strings.EqualFold(prior.SecondaryThumbprint, fresh.SecondaryThumbprint) {
		out = append(out, fmt.Sprintf("authentication.secondary_thumbprint: %q → %q", prior.SecondaryThumbprint, fresh.SecondaryThumbprint))
	}
	return out
}

// DiffString is a helper for resource-specific written fields.
func DiffString(out []string, name, prior, fresh string) []string {
	if prior != fresh {
		out = append(out, fmt.Sprintf("%s: %q → %q", name, prior, fresh))
	}
	return out
}
