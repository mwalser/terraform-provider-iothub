package twinpatch

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func doc(t *testing.T, s string) map[string]any {
	t.Helper()
	if s == "" {
		return nil
	}
	d, err := Decode(s)
	if err != nil {
		t.Fatalf("decode %q: %v", s, err)
	}
	return d
}

func enc(v map[string]any) string {
	if v == nil {
		return "null"
	}
	return Encode(v)
}

func TestLeaves(t *testing.T) {
	d := doc(t, `{"site":"munich","fleet":{"region":"eu","ring":2,"tags":["a","b"],"opts":{}},"empty":{},"n":{"m":{"o":1}}}`)
	got := SortedPaths(Leaves(d))
	want := []string{"empty", "fleet.opts", "fleet.region", "fleet.ring", "fleet.tags", "n.m.o", "site"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("leaves = %v, want %v", got, want)
	}
	if len(Leaves(nil)) != 0 || len(Leaves(map[string]any{})) != 0 {
		t.Error("empty docs have no leaves")
	}
}

func TestProject(t *testing.T) {
	remote := doc(t, `{"site":"munich","fleet":{"region":"eu","ring":2,"lastCheck":"x"},"fw":{"channel":"stable","build":7},"scalar":{"a":1},"objempty":"str","gone":{"z":1}}`)
	owned := Leaves(doc(t, `{"site":"berlin","fleet":{"region":"eu"},"fw":{},"scalar":1,"objempty":{},"missing":true,"gone":{"y":2}}`))
	got := Project(remote, owned)
	want := doc(t, `{"site":"munich","fleet":{"region":"eu"},"fw":{},"scalar":{"a":1},"objempty":"str"}`)
	if !Equal(got, want) {
		t.Errorf("project = %s, want %s", enc(got), enc(want))
	}
	// projections are copies
	Set(got, Path{"fleet", "region"}, "changed")
	if v, _ := Get(remote, Path{"fleet", "region"}); v != "eu" {
		t.Error("projection must not alias the remote document")
	}
}

func TestDiff(t *testing.T) {
	cases := []struct {
		name, prev, next, remote, want string
	}{
		{"create sets everything not already there", ``, `{"a":1,"b":{"c":2,"d":[1,2]}}`, `{"b":{"c":2},"z":0}`, `{"a":1,"b":{"d":[1,2]}}`},
		{"no change", `{"a":1}`, `{"a":1}`, `{"a":1}`, `null`},
		{"changed leaf", `{"a":1}`, `{"a":2}`, `{"a":1}`, `{"a":2}`},
		{"changed leaf, remote already there", `{"a":1}`, `{"a":2}`, `{"a":2}`, `null`},
		{"numbers compare by value", `{"a":1}`, `{"a":1.0}`, `{"a":1}`, `null`},
		{"array replaced whole", `{"a":[1,2]}`, `{"a":[1,2,3]}`, `{"a":[1,2]}`, `{"a":[1,2,3]}`},
		{"removed leaf keeps foreign sibling", `{"a":{"b":1}}`, `{}`, `{"a":{"b":1,"z":9}}`, `{"a":{"b":null}}`},
		{"removed leaf keeps owned sibling", `{"a":{"b":1,"c":2}}`, `{"a":{"c":2}}`, `{"a":{"b":1,"c":2}}`, `{"a":{"b":null}}`},
		{"removed last leaves null the object", `{"a":{"b":1,"c":2}}`, `{}`, `{"a":{"b":1,"c":2}}`, `{"a":null}`},
		{"removed subtree nulls highest owned ancestor", `{"a":{"b":{"c":1,"d":2}},"k":1}`, `{"k":1}`, `{"a":{"b":{"c":1,"d":2}}}`, `{"a":null}`},
		{"removed subtree, ancestor shared with foreign", `{"a":{"b":{"c":1,"d":2}}}`, `{}`, `{"a":{"b":{"c":1,"d":2},"x":1}}`, `{"a":{"b":null}}`},
		{"removed leaf already gone remotely", `{"a":{"b":1}}`, `{}`, `{"a":{"z":1}}`, `null`},
		{"removed leaf, ancestor gone remotely", `{"a":{"b":1}}`, `{}`, `{}`, `null`},
		{"one removed leaf gone, sibling present", `{"a":{"b":1,"c":2}}`, `{}`, `{"a":{"c":2}}`, `{"a":null}`},
		{"removed leaf whose remote became foreign object", `{"a":{"b":1}}`, `{}`, `{"a":{"b":{"x":1}}}`, `null`},
		{"removed {} leaf", `{"fw":{}}`, `{}`, `{"fw":{}}`, `{"fw":null}`},
		{"removed {} leaf with foreign content", `{"fw":{}}`, `{}`, `{"fw":{"x":1}}`, `null`},
		{"{} becomes owned content", `{"fw":{}}`, `{"fw":{"ch":"s"}}`, `{"fw":{}}`, `{"fw":{"ch":"s"}}`},
		{"owned content becomes {}", `{"fw":{"ch":"s"}}`, `{"fw":{}}`, `{"fw":{"ch":"s"}}`, `{"fw":{"ch":null}}`},
		{"owned content becomes {} with foreign sibling", `{"fw":{"ch":"s"}}`, `{"fw":{}}`, `{"fw":{"ch":"s","x":1}}`, `{"fw":{"ch":null}}`},
		{"scalar becomes object", `{"a":1}`, `{"a":{"b":2}}`, `{"a":1}`, `{"a":{"b":2}}`},
		{"object becomes scalar", `{"a":{"b":2}}`, `{"a":1}`, `{"a":{"b":2}}`, `{"a":1}`},
		{"object becomes scalar with foreign sibling: replaced (owner asserts a)", `{"a":{"b":2}}`, `{"a":1}`, `{"a":{"b":2,"z":1}}`, `{"a":1}`},
		{"drifted shape restored", `{"a":{"b":{"x":1}}}`, `{"a":{"b":1}}`, `{"a":{"b":{"x":1}}}`, `{"a":{"b":1}}`},
		{"mixed", `{"a":1,"b":{"c":2,"d":3},"e":{"f":{"g":4}}}`, `{"a":1,"b":{"c":20},"h":true}`, `{"a":1,"b":{"c":2,"d":3},"e":{"f":{"g":4}},"other":1}`,
			`{"b":{"c":20,"d":null},"e":null,"h":true}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Diff(doc(t, tc.prev), doc(t, tc.next), doc(t, tc.remote))
			if tc.want == "null" {
				if got != nil {
					t.Errorf("Diff = %s, want nil", enc(got))
				}
				return
			}
			if got == nil || !Equal(got, doc(t, tc.want)) {
				t.Errorf("Diff(prev=%s next=%s remote=%s) = %s, want %s", tc.prev, tc.next, tc.remote, enc(got), tc.want)
			}
		})
	}
}

// applyMergePatch is RFC 7386, used to check that patches produce the
// intended remote document.
func applyMergePatch(target any, patch any) any {
	po, ok := patch.(map[string]any)
	if !ok {
		return patch
	}
	to, ok := target.(map[string]any)
	if !ok {
		to = map[string]any{}
	}
	for k, v := range po {
		if v == nil {
			delete(to, k)
		} else {
			to[k] = applyMergePatch(to[k], v)
		}
	}
	return to
}

func TestDiff_RoundTripAgainstMergePatch(t *testing.T) {
	// After applying the patch, the projection of the owned (next) leaves must
	// equal next, and foreign leaves must be untouched.
	cases := []struct{ prev, next, remote string }{
		{`{"a":{"b":1}}`, `{}`, `{"a":{"b":1,"z":9}}`},
		{`{"a":{"b":{"c":1}}}`, `{"a":{"b":{"d":2}}}`, `{"a":{"b":{"c":1},"z":9},"y":{"q":1}}`},
		{`{"fw":{"ch":"s"}}`, `{"fw":{}}`, `{"fw":{"ch":"s","x":1}}`},
		{`{}`, `{"a":{"b":[1,{"c":2}]}}`, `{"a":"scalar"}`},
		{`{"a":1,"b":{"c":2,"d":3},"e":{"f":{"g":4}}}`, `{"a":1,"b":{"c":20},"h":true}`, `{"a":1,"b":{"c":2,"d":3},"e":{"f":{"g":4}},"other":1}`},
	}
	for _, tc := range cases {
		prev, next, remote := doc(t, tc.prev), doc(t, tc.next), doc(t, tc.remote)
		foreignBefore := foreignLeaves(remote, prev)
		patch := Diff(prev, next, remote)
		after, _ := applyMergePatch(Clone(remote), patch).(map[string]any)
		if got := Project(after, Leaves(next)); !Equal(got, next) {
			t.Errorf("after patch %s: projection %s != next %s", enc(patch), enc(got), tc.next)
		}
		for k, v := range foreignBefore {
			// foreign leaves survive unless a next leaf sits above them (replaces
			// the subtree) or below them (turns the scalar into an object)
			replaced := false
			for _, n := range Leaves(next) {
				if v.Path.HasPrefix(n.Path) || n.Path.HasPrefix(v.Path) {
					replaced = true
				}
			}
			if replaced {
				continue
			}
			if got, ok := Get(after, v.Path); !ok || !Equal(got, v.Value) {
				t.Errorf("foreign leaf %s changed by patch %s: %v", k, enc(patch), got)
			}
		}
	}
}

func foreignLeaves(remote, owned map[string]any) map[string]Leaf {
	out := map[string]Leaf{}
	o := Leaves(owned)
	for k, l := range Leaves(remote) {
		if _, ok := o[k]; !ok {
			out[k] = l
		}
	}
	return out
}

func TestEqual(t *testing.T) {
	yes := [][2]string{{`1`, `1.0`}, {`1e2`, `100`}, {`"a"`, `"a"`}, {`[1,{"a":2}]`, `[1.0,{"a":2.0}]`}, {`{}`, `{}`}, {`true`, `true`}}
	no := [][2]string{{`1`, `"1"`}, {`[1]`, `[1,1]`}, {`{"a":1}`, `{"a":1,"b":2}`}, {`{"a":1}`, `{}`}, {`null`, `0`}, {`1.5`, `1.50001`}}
	dec := func(s string) any {
		d := json.NewDecoder(strings.NewReader(s))
		d.UseNumber()
		var v any
		_ = d.Decode(&v)
		return v
	}
	for _, c := range yes {
		if !Equal(dec(c[0]), dec(c[1])) {
			t.Errorf("%s == %s expected", c[0], c[1])
		}
	}
	for _, c := range no {
		if Equal(dec(c[0]), dec(c[1])) {
			t.Errorf("%s != %s expected", c[0], c[1])
		}
	}
}

func TestDecodeEncodeStrip(t *testing.T) {
	if _, err := Decode(`[1]`); err == nil || !strings.Contains(err.Error(), "object") {
		t.Errorf("array must be rejected: %v", err)
	}
	if _, err := Decode(`{"a":1} x`); err == nil {
		t.Error("trailing data must be rejected")
	}
	d, err := Decode(`{"b":12345678901234567890,"a":{"$metadata":{},"$version":3,"x":"<y>"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := Encode(d); got != `{"a":{"$metadata":{},"$version":3,"x":"<y>"},"b":12345678901234567890}` {
		t.Errorf("encode = %s", got)
	}
	a, _ := d["a"].(map[string]any)
	s := StripSystem(a)
	if got := Encode(s); got != `{"x":"<y>"}` {
		t.Errorf("strip = %s", got)
	}
	if Encode(nil) != "{}" {
		t.Error("nil encodes as {}")
	}
}

func TestValidate(t *testing.T) {
	nest := func(levels int) string {
		s := "1"
		for i := levels; i >= 1; i-- {
			s = `{"l` + itoa(i) + `":` + s + `}`
		}
		return s
	}
	ok := []string{`{}`, `{"a":1,"b":{"c":[1,{"d":true}],"e":{}}}`, nest(11), `{"ünï":1,"":1}`, `{"s":"` + strings.Repeat("x", 4096) + `"}`}
	for _, s := range ok {
		if p := Validate(doc(t, s)); len(p) != 0 {
			t.Errorf("%s: unexpected problems %v", s[:min(len(s), 40)], p)
		}
	}
	bad := map[string]string{
		`{"a.b":1}`:        "key must not contain",
		`{"a$b":1}`:        "key must not contain",
		`{"a#b":1}`:        "key must not contain",
		`{"a b":1}`:        "key must not contain",
		`{"a\u0001b":1}`:   "control characters",
		`{"a":null}`:       "null is not a twin value",
		`{"a":{"b":null}}`: "null is not a twin value",
		`{"a":[1,null]}`:   "array element 1 is null",
		`{"s":"` + strings.Repeat("x", 4097) + `"}`: "string value is 4097 bytes",
		`{"` + strings.Repeat("k", 1025) + `":1}`:   "key is 1025 bytes",
		nest(12): "nest at most 10 levels",
	}
	for s, want := range bad {
		p := Validate(doc(t, s))
		if len(p) == 0 {
			t.Errorf("%s: expected a problem containing %q", s[:min(len(s), 40)], want)
			continue
		}
		found := false
		for _, x := range p {
			if strings.Contains(x.String(), want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: problems %v do not mention %q", s[:min(len(s), 40)], p, want)
		}
	}
	// nested key problems carry the path
	p := Validate(doc(t, `{"a":{"b c":1}}`))
	if len(p) != 1 || p[0].Path.String() != "a.b c" {
		t.Errorf("path in problem: %v", p)
	}
}

func itoa(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}

func TestHasPrefix(t *testing.T) {
	if !(Path{"a", "b"}).HasPrefix(Path{"a"}) || (Path{"a"}).HasPrefix(Path{"a", "b"}) || !(Path{"a"}).HasPrefix(Path{"a"}) || (Path{"x", "b"}).HasPrefix(Path{"a"}) {
		t.Error("HasPrefix")
	}
}

func TestValidatePatch(t *testing.T) {
	// nulls as object values are allowed (they delete keys), everything else is
	// checked as by Validate
	for _, s := range []string{`{"a":null}`, `{"a":{"b":null},"c":1}`} {
		if p := ValidatePatch(doc(t, s)); len(p) != 0 {
			t.Errorf("%s: unexpected problems %v", s, p)
		}
	}
	for s, want := range map[string]string{
		`{"a":[1,null]}`: "array element 1 is null",
		`{"a.b":null}`:   "key must not contain",
	} {
		p := ValidatePatch(doc(t, s))
		if len(p) == 0 || !strings.Contains(p[0].Message, want) {
			t.Errorf("%s: expected a problem containing %q, got %v", s, want, p)
		}
	}
	// and Validate still rejects nulls
	if p := Validate(doc(t, `{"a":null}`)); len(p) == 0 {
		t.Error("Validate must still reject nulls")
	}
}
