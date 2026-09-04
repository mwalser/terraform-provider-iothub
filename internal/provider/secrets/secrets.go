// Package secrets implements the placeholder tokens of CONCEPT.md §6.7. A
// string value of a JSON document that is exactly `{{secret.<name>}}` stands
// for the value stored under <name> in a secrets map (the write-only
// `secrets_wo` of iothub_edge_deployment, `secrets` of
// iothub_set_edge_modules). Documents are resolved on their way to the hub
// and tokenized again on their way back, so a secret never enters state,
// the plan or a diagnostic — and the rest of the document keeps its diff.
// A Fingerprint of the hub's values, one salted hash kept in state, lets
// the plan tell a changed value without reading the hub.
//
// Only whole string values are tokens: there is no substitution inside a
// longer string, so JSON escaping inside `createOptions` strings and the
// edge agent's 512-character chunking never come into play.
package secrets

import (
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/mwalser/terraform-provider-iothub/internal/provider/jsondoc"
	"github.com/mwalser/terraform-provider-iothub/internal/twinpatch"
)

const nameChars = `[A-Za-z0-9_-]+`

// namePattern is the charset of a secret name — a key of the secrets map
// and the <name> of a token.
var namePattern = regexp.MustCompile(`^` + nameChars + `$`)

// NameDescription describes the name charset for messages.
const NameDescription = "letters, digits, `_` and `-`"

// docAttr is the document attribute of both callers, the one that holds the
// tokens; the secrets attribute differs between them, so it is a parameter.
const docAttr = "modules_content"

// tokenPattern matches a whole token and captures its name.
var tokenPattern = regexp.MustCompile(`^\{\{secret\.(` + nameChars + `)\}\}$`)

// MapValidators are the rules of a secrets map: at least one key, names of
// NameDescription.
func MapValidators() []validator.Map {
	return []validator.Map{
		mapvalidator.SizeAtLeast(1),
		mapvalidator.KeysAre(stringvalidator.RegexMatches(namePattern, "must be "+NameDescription)),
	}
}

// Token renders the placeholder for name.
func Token(name string) string { return "{{secret." + name + "}}" }

// Name returns the secret a string refers to, if the string is a token.
func Name(s string) (string, bool) {
	m := tokenPattern.FindStringSubmatch(s)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// isToken reports whether s is a token.
func isToken(s string) bool {
	_, ok := Name(s)
	return ok
}

// Ref is a token found in a document.
type Ref struct {
	Path string // rendered for people: keys joined by ".", array elements as [i]
	Name string
	segs docPath
}

// Refs lists the tokens of doc in a stable order: keys sorted, array
// elements by index.
func Refs(doc map[string]any) []Ref {
	var out []Ref
	walk(doc, nil, func(p docPath, s string) {
		if n, ok := Name(s); ok {
			out = append(out, Ref{Path: p.String(), Name: n, segs: p})
		}
	})
	return out
}

// IsLiteral reports whether doc holds a plain string, not a token, at r's
// path.
func (r Ref) IsLiteral(doc map[string]any) bool {
	s, ok := r.segs.stringAt(doc)
	return ok && !isToken(s)
}

// Resolve returns a deep copy of doc in which every token whose name has a
// value is replaced by that value. Tokens without a value stay as they are.
func Resolve(doc map[string]any, values map[string]string) map[string]any {
	out, _ := transform(doc, func(s string) string {
		if n, ok := Name(s); ok {
			if v, ok := values[n]; ok {
				return v
			}
		}
		return s
	}).(map[string]any)
	return out
}

// ResolveJSON is Resolve for an encoded document, as it goes to the hub.
// The document always comes back, resolved as far as the values go (Update
// compares it that way); unresolved names the first token without a value
// and means the document must not be written. A document without tokens,
// or one that does not decode (its own validation reports that), is
// returned verbatim; one with tokens is re-encoded (compact, sorted keys).
func ResolveJSON(s string, values map[string]string) (resolved string, unresolved error) {
	doc, _ := twinpatch.Decode(s) // undecodable: no document, no tokens, passed through
	refs := Refs(doc)
	if len(refs) == 0 {
		return s, nil
	}
	var err error
	for _, r := range refs {
		if _, ok := values[r.Name]; !ok {
			err = fmt.Errorf("`%s` at %s has no value", Token(r.Name), r.Path)
			break
		}
	}
	return twinpatch.Encode(Resolve(doc, values)), err
}

// Unresolved is the diagnostic for a ResolveJSON error: the document
// attribute refers to a placeholder the secrets map does not fill.
func Unresolved(err error) diag.Diagnostic {
	return diag.NewAttributeErrorDiagnostic(path.Root(docAttr), "Secret placeholder without a value", err.Error()+".")
}

// Retokenize returns a deep copy of doc in which every string that sits at a
// path where prior holds a token becomes that token again. A path that
// vanished or no longer holds a string keeps doc's value.
func Retokenize(doc, prior map[string]any) map[string]any {
	out, _ := retokenize(doc, prior, func(string) bool { return true }).(map[string]any)
	return out
}

// AdoptTokens returns a deep copy of doc in which a token at a path where
// next also holds a token becomes next's token: a renamed placeholder, whose
// value the record compares by path. Literals and everything else stay.
func AdoptTokens(doc, next map[string]any) map[string]any {
	out, _ := retokenize(doc, next, isToken).(map[string]any)
	return out
}

// ---- the secrets map ------------------------------------------------------------

// Values reads a map(string) attribute: the known values by key, and
// whether the map and all its values are known. A null map has no values
// and is known; a null value is skipped (Validate reports it).
func Values(m types.Map) (values map[string]string, known bool) {
	if m.IsUnknown() {
		return nil, false
	}
	values, known = map[string]string{}, true
	for k, v := range m.Elements() {
		s, ok := v.(types.String)
		switch {
		case !ok || s.IsUnknown():
			known = false
		case s.IsNull():
		default:
			values[k] = s.ValueString()
		}
	}
	return values, known
}

// Validate checks the correspondence between the tokens of doc (the
// document attribute) and the keys of the secrets attribute mapAttr: every
// token needs a key, every key needs a token, and no value is null. Unknown
// values are skipped.
func Validate(doc jsondoc.Value, m types.Map, mapAttr string) diag.Diagnostics {
	var diags diag.Diagnostics
	if m.IsUnknown() || doc.IsUnknown() {
		return diags
	}
	d, err := doc.Object() // nil for a null document
	if err != nil {
		return diags // reported by the document's own validation
	}
	refs := Refs(d)
	if m.IsNull() {
		if len(refs) > 0 {
			diags.AddAttributeError(path.Root(mapAttr), "Secret placeholder without a value",
				fmt.Sprintf("`%s` refers to `%s` at %s, but `%s` is not set.", docAttr, Token(refs[0].Name), refs[0].Path, mapAttr))
		}
		return diags
	}
	elems := m.Elements()
	if len(elems) == 0 {
		return diags // reported by the attribute's size validator
	}
	used := map[string]bool{}
	for _, r := range refs {
		used[r.Name] = true
		if _, ok := elems[r.Name]; !ok {
			diags.AddAttributeError(path.Root(mapAttr), "Secret placeholder without a value",
				fmt.Sprintf("`%s` refers to `%s` at %s, but `%s` has no key `%s`.", docAttr, Token(r.Name), r.Path, mapAttr, r.Name))
		}
	}
	for _, k := range slices.Sorted(maps.Keys(elems)) {
		switch {
		case !used[k]:
			diags.AddAttributeError(path.Root(mapAttr).AtMapKey(k), "Unused secret",
				fmt.Sprintf("No string value in `%s` is `%s`. Remove the key or reference it. A placeholder inside a longer string, such as `createOptions`, is not substituted.", docAttr, Token(k)))
		case elems[k].IsNull():
			diags.AddAttributeError(path.Root(mapAttr).AtMapKey(k), "Null secret", "A secret value must not be null.")
		}
	}
	return diags
}

// ---- walking ------------------------------------------------------------------------

// docPath addresses a value inside a document: object keys and array
// indices — unlike twinpatch.Path, which has no arrays.
type docPath []any

// child returns the path one segment below p.
func (p docPath) child(s any) docPath {
	out := make(docPath, len(p)+1)
	copy(out, p)
	out[len(p)] = s
	return out
}

// String renders the path for people: keys joined by ".", array elements
// as [i].
func (p docPath) String() string {
	var b strings.Builder
	for i, s := range p {
		switch t := s.(type) {
		case string:
			if i > 0 {
				b.WriteByte('.')
			}
			b.WriteString(t)
		case int:
			b.WriteString("[" + strconv.Itoa(t) + "]")
		}
	}
	return b.String()
}

// key identifies the path in a fingerprint: the segments as JSON, so a
// dotted key and a nested one never collide.
func (p docPath) key() string {
	b, _ := json.Marshal([]any(p)) // strings and ints cannot fail to encode
	return string(b)
}

// get returns the value at p, if the path exists.
func (p docPath) get(v any) (any, bool) {
	for _, s := range p {
		switch t := v.(type) {
		case map[string]any:
			k, ok := s.(string)
			if !ok {
				return nil, false
			}
			if v, ok = t[k]; !ok {
				return nil, false
			}
		case []any:
			i, ok := s.(int)
			if !ok || i < 0 || i >= len(t) {
				return nil, false
			}
			v = t[i]
		default:
			return nil, false
		}
	}
	return v, true
}

// stringAt returns the string at p, if the path exists and holds one.
func (p docPath) stringAt(doc map[string]any) (string, bool) {
	v, ok := p.get(doc)
	s, isString := v.(string)
	return s, ok && isString
}

// walk calls fn for every string in v with its path; keys sorted, arrays
// by index.
func walk(v any, p docPath, fn func(p docPath, s string)) {
	switch t := v.(type) {
	case map[string]any:
		for _, k := range slices.Sorted(maps.Keys(t)) {
			walk(t[k], p.child(k), fn)
		}
	case []any:
		for i, e := range t {
			walk(e, p.child(i), fn)
		}
	case string:
		fn(p, t)
	}
}

// transform deep-copies v, applying fn to every string.
func transform(v any, fn func(string) string) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = transform(e, fn)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = transform(e, fn)
		}
		return out
	case string:
		return fn(t)
	default:
		return v // json.Number, bool, nil: immutable
	}
}

// retokenize copies v, replacing a string that replaces accepts by the
// token tokens holds at the same path — every string (Retokenize), or only
// tokens (AdoptTokens).
func retokenize(v, tokens any, replaces func(string) bool) any {
	switch t := v.(type) {
	case map[string]any:
		tm, _ := tokens.(map[string]any)
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = retokenize(e, tm[k], replaces)
		}
		return out
	case []any:
		ta, _ := tokens.([]any)
		out := make([]any, len(t))
		for i, e := range t {
			var te any
			if i < len(ta) {
				te = ta[i]
			}
			out[i] = retokenize(e, te, replaces)
		}
		return out
	case string:
		ts, ok := tokens.(string)
		if !ok || !isToken(ts) || !replaces(t) {
			return t
		}
		return ts
	default:
		return v
	}
}
