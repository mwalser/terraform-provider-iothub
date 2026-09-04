package secrets

import (
	"reflect"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/mwalser/terraform-provider-iothub/internal/provider/jsondoc"
	"github.com/mwalser/terraform-provider-iothub/internal/twinpatch"
)

func decode(t *testing.T, s string) map[string]any {
	t.Helper()
	d, err := twinpatch.Decode(s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

const manifest = `{"$edgeAgent":{"properties.desired":{"runtime":{"settings":{"registryCredentials":{"acr":{"address":"contoso.azurecr.io","username":"ci","password":"{{secret.acr}}"}}}},
"modules":{"collector":{"env":{"CONN":{"value":"{{secret.storage}}"},"LIST":{"value":["a","{{secret.acr}}"]}},"settings":{"createOptions":"{\"Env\":[\"X={{secret.acr}}\"]}"}}}}}}`

var values = map[string]string{"acr": "p4ss", "storage": "Endpoint=sb://x;Key=y"}

func TestNameAndToken(t *testing.T) {
	for s, want := range map[string]string{"{{secret.acr}}": "acr", "{{secret.a-b_9}}": "a-b_9"} {
		if n, ok := Name(s); !ok || n != want {
			t.Errorf("Name(%q) = %q, %v", s, n, ok)
		}
		if Token(want) != s {
			t.Errorf("Token(%q) = %q", want, Token(want))
		}
	}
	for _, s := range []string{"", "acr", "{{secret.acr}} ", " {{secret.acr}}", "x{{secret.acr}}", "{{ secret.acr }}", "{{secret.}}", "{{secret.a.b}}", "{{secrets.acr}}", "${secret.acr}"} {
		if _, ok := Name(s); ok {
			t.Errorf("Name(%q) accepted", s)
		}
	}
}

func TestRefs(t *testing.T) {
	var got []string
	for _, r := range Refs(decode(t, manifest)) {
		got = append(got, r.Path+"="+r.Name)
	}
	want := []string{
		"$edgeAgent.properties.desired.modules.collector.env.CONN.value=storage",
		"$edgeAgent.properties.desired.modules.collector.env.LIST.value[1]=acr",
		"$edgeAgent.properties.desired.runtime.settings.registryCredentials.acr.password=acr",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Refs = %v", got)
	}
}

func TestRefIsLiteral(t *testing.T) {
	doc := decode(t, manifest)
	r := Refs(doc)[0]
	if r.IsLiteral(doc) {
		t.Error("a token is not a literal")
	}
	if !r.IsLiteral(Resolve(doc, values)) {
		t.Error("a resolved value is a literal")
	}
	if r.IsLiteral(map[string]any{}) {
		t.Error("a missing path is not a literal")
	}
}

func TestResolve(t *testing.T) {
	doc := decode(t, manifest)
	resolved := Resolve(doc, values)
	enc := twinpatch.Encode(resolved)
	if len(Refs(resolved)) != 0 || !strings.Contains(enc, `"password":"p4ss"`) || !strings.Contains(enc, `["a","p4ss"]`) || !strings.Contains(enc, `Endpoint=sb://x;Key=y`) {
		t.Errorf("Resolve: %s", enc)
	}
	if !strings.Contains(enc, `X={{secret.acr}}`) {
		t.Error("a token inside a longer string (createOptions) must not be substituted")
	}
	if !twinpatch.Equal(doc, decode(t, manifest)) {
		t.Error("Resolve modified its input")
	}
	partial := Resolve(doc, map[string]string{"acr": "p4ss"})
	if !strings.Contains(twinpatch.Encode(partial), `"value":"{{secret.storage}}"`) {
		t.Error("a token without a value must stay")
	}
}

func TestResolveJSON(t *testing.T) {
	if s, err := ResolveJSON(manifest, values); err != nil || s != twinpatch.Encode(Resolve(decode(t, manifest), values)) {
		t.Errorf("resolved: %v %s", err, s)
	}
	for _, verbatim := range []string{"not json", `{ "a" : 1 }`} { // undecodable, and without tokens
		if s, err := ResolveJSON(verbatim, values); err != nil || s != verbatim {
			t.Errorf("%q: %v %q", verbatim, err, s)
		}
	}
	s, err := ResolveJSON(manifest, map[string]string{"acr": "p4ss"})
	if err == nil || !strings.Contains(err.Error(), "`{{secret.storage}}` at $edgeAgent.properties.desired.modules.collector.env.CONN.value has no value") {
		t.Errorf("unresolved token: %v", err)
	}
	if !strings.Contains(s, `"password":"p4ss"`) || !strings.Contains(s, `"{{secret.storage}}"`) {
		t.Errorf("resolved as far as the values go: %s", s)
	}
	// A value that is itself shaped like a token is a value, not a leftover.
	shaped := map[string]string{"acr": "{{secret.other}}", "storage": "c"}
	if s, err := ResolveJSON(manifest, shaped); err != nil || !strings.Contains(s, `"password":"{{secret.other}}"`) {
		t.Errorf("token-shaped value: %v %s", err, s)
	}
}

func TestRetokenizeAndAdoptTokens(t *testing.T) {
	doc := decode(t, manifest)
	resolved := Resolve(doc, values)
	if back := Retokenize(resolved, doc); !twinpatch.Equal(back, doc) {
		t.Errorf("Retokenize: %s", twinpatch.Encode(back))
	}
	if !twinpatch.Equal(resolved, decode(t, twinpatch.Encode(resolved))) {
		t.Error("Retokenize modified its input")
	}
	// A non-string at a token path keeps the hub's shape (drift).
	drifted := Resolve(doc, values)
	p := twinpatch.Path{"$edgeAgent", "properties.desired", "modules", "collector", "env", "CONN", "value"}
	twinpatch.Set(drifted, p, map[string]any{"nested": true})
	if v, _ := twinpatch.Get(Retokenize(drifted, doc), p); !reflect.DeepEqual(v, map[string]any{"nested": true}) {
		t.Errorf("non-string at a token path must be kept: %v", v)
	}
	if !twinpatch.Equal(Retokenize(resolved, nil), resolved) {
		t.Error("Retokenize without a prior document must be a copy")
	}
	// AdoptTokens takes the next document's token where both hold one, and
	// leaves literals alone.
	next := decode(t, strings.ReplaceAll(manifest, `"{{secret.acr}}"`, `"{{secret.registry}}"`)) // whole values only; createOptions keeps its text
	adopted := AdoptTokens(doc, next)
	if !twinpatch.Equal(adopted, next) {
		t.Errorf("AdoptTokens: %s", twinpatch.Encode(adopted))
	}
	if !twinpatch.Equal(AdoptTokens(resolved, next), resolved) {
		t.Error("AdoptTokens must leave literals alone")
	}
}

func strMap(elems map[string]attr.Value) types.Map {
	return types.MapValueMust(types.StringType, elems)
}

func TestValues(t *testing.T) {
	if v, known := Values(strMap(map[string]attr.Value{"b": types.StringValue("2"), "a": types.StringUnknown()})); known || !reflect.DeepEqual(v, map[string]string{"b": "2"}) {
		t.Errorf("partly unknown: %v %v", v, known)
	}
	if v, known := Values(strMap(map[string]attr.Value{"b": types.StringValue("2"), "a": types.StringNull()})); !known || !reflect.DeepEqual(v, map[string]string{"b": "2"}) {
		t.Errorf("a null value has no value: %v %v", v, known)
	}
	if v, known := Values(types.MapNull(types.StringType)); !known || len(v) != 0 {
		t.Errorf("null: %v %v", v, known)
	}
	if v, known := Values(types.MapUnknown(types.StringType)); known || v != nil {
		t.Errorf("unknown: %v %v", v, known)
	}
}

func TestValidate(t *testing.T) {
	docType := jsondoc.Type{Name: "test"}
	doc := jsondoc.NewValue(docType, manifest)
	noTokens := jsondoc.NewValue(docType, `{"$edgeAgent":{"properties.desired":{}}}`)
	insideCreateOptions := jsondoc.NewValue(docType, `{"$edgeAgent":{"properties.desired":{"settings":{"createOptions":"{\"Env\":[\"X={{secret.acr}}\"]}"}}}}`)
	full := strMap(map[string]attr.Value{"acr": types.StringValue("p"), "storage": types.StringValue("c")})
	cases := []struct {
		name string
		doc  jsondoc.Value
		m    types.Map
		want []string // substrings of the diagnostics, in order; none: no error
	}{
		{"ok", doc, full, nil},
		{"unknown map", doc, types.MapUnknown(types.StringType), nil},
		{"unknown doc", jsondoc.Value{StringValue: types.StringUnknown()}, full, nil},
		{"unknown value", doc, strMap(map[string]attr.Value{"acr": types.StringUnknown(), "storage": types.StringValue("c")}), nil},
		{"null map with tokens", doc, types.MapNull(types.StringType), []string{"`secrets_wo` is not set"}},
		{"null map without tokens", noTokens, types.MapNull(types.StringType), nil},
		{"empty map", noTokens, strMap(map[string]attr.Value{}), nil}, // the schema's size validator reports it
		{"missing key", doc, strMap(map[string]attr.Value{"acr": types.StringValue("p")}), []string{"has no key `storage`"}},
		{"unused key", doc, strMap(map[string]attr.Value{"acr": types.StringValue("p"), "storage": types.StringValue("c"), "extra": types.StringValue("x")}), []string{"Unused secret"}},
		{"token only inside createOptions", insideCreateOptions, strMap(map[string]attr.Value{"acr": types.StringValue("p")}), []string{"Unused secret"}},
		{"null value", doc, strMap(map[string]attr.Value{"acr": types.StringNull(), "storage": types.StringValue("c")}), []string{"Null secret"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			diags := Validate(c.doc, c.m, "secrets_wo")
			if len(diags) != len(c.want) {
				t.Fatalf("got %d diagnostics %v, want %d", len(diags), diags, len(c.want))
			}
			for i, w := range c.want {
				if s := diags[i].Summary() + " " + diags[i].Detail(); !strings.Contains(s, w) {
					t.Errorf("diagnostic %d = %q, want %q", i, s, w)
				}
			}
		})
	}
}

// TestRetokenizeEdges: an empty value is a string like any other, and the
// hub's arrays keep their own length.
func TestRetokenizeEdges(t *testing.T) {
	doc := decode(t, manifest)
	empty := map[string]string{"acr": "", "storage": values["storage"]}
	resolved := Resolve(doc, empty)
	if enc := twinpatch.Encode(resolved); !strings.Contains(enc, `"password":""`) || !strings.Contains(enc, `["a",""]`) {
		t.Fatalf("empty value: %s", enc)
	}
	if back := Retokenize(resolved, doc); !twinpatch.Equal(back, doc) {
		t.Errorf("an empty string at a token path is tokenized: %s", twinpatch.Encode(back))
	}
	listPath := twinpatch.Path{"$edgeAgent", "properties.desired", "modules", "collector", "env", "LIST", "value"}
	shorter := Resolve(doc, values)
	twinpatch.Set(shorter, listPath, []any{"a"})
	if v, _ := twinpatch.Get(Retokenize(shorter, doc), listPath); !reflect.DeepEqual(v, []any{"a"}) {
		t.Errorf("a shorter hub array keeps what it has: %v", v)
	}
	longer := Resolve(doc, values)
	twinpatch.Set(longer, listPath, []any{"a", values["acr"], "extra"})
	if v, _ := twinpatch.Get(Retokenize(longer, doc), listPath); !reflect.DeepEqual(v, []any{"a", "{{secret.acr}}", "extra"}) {
		t.Errorf("a longer hub array keeps its extra elements: %v", v)
	}
}
