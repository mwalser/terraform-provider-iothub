package secrets

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mwalser/terraform-provider-iothub/internal/twinpatch"
)

// deleteAt removes the key at the end of p from doc.
func deleteAt(t *testing.T, doc map[string]any, p twinpatch.Path) {
	t.Helper()
	parent, ok := twinpatch.Get(doc, p[:len(p)-1])
	obj, isObj := parent.(map[string]any)
	if !ok || !isObj {
		t.Fatalf("no object at %s", p[:len(p)-1])
	}
	delete(obj, p[len(p)-1])
}

func TestFingerprint_TakeAndRoundTrip(t *testing.T) {
	refs := Refs(decode(t, manifest))
	hub := Resolve(decode(t, manifest), values)
	f := Fingerprint{}.Take(refs, hub)
	s := f.String()
	if s == "" || len(f.salt) != saltLen || len(f.hash) != hashLen {
		t.Fatalf("fingerprint: %q %+v", s, f)
	}
	for _, secret := range values {
		if strings.Contains(s, secret) {
			t.Fatalf("a value leaked into the fingerprint: %s", s)
		}
	}
	loaded := ParseFingerprint(s)
	if !bytes.Equal(loaded.salt, f.salt) || !bytes.Equal(loaded.hash, f.hash) {
		t.Fatalf("round trip: %+v", loaded)
	}
	if !loaded.Matches(refs, values) {
		t.Error("the same values must match")
	}
	if loaded.Matches(refs, map[string]string{"acr": "rotated", "storage": values["storage"]}) {
		t.Error("a rotated value must not match")
	}
	if loaded.Matches(refs, map[string]string{"storage": values["storage"]}) {
		t.Error("a name without a value must not match")
	}
	if loaded.Matches(refs[:1], values) {
		t.Error("fewer paths must not match")
	}
	for _, bad := range []string{"", "{", "AAAA"} {
		if p := ParseFingerprint(bad); p.String() != "" || p.Matches(refs, values) {
			t.Errorf("%q must parse as the empty fingerprint that matches nothing", bad)
		}
	}
}

func TestFingerprint_FollowsTheHub(t *testing.T) {
	refs := Refs(decode(t, manifest))
	hub := Resolve(decode(t, manifest), values)
	f := Fingerprint{}.Take(refs, hub)
	// Unchanged values: the same string, byte for byte.
	if f.Take(refs, hub).String() != f.String() {
		t.Error("an unchanged hub must give the same fingerprint")
	}
	// A changed value: the fingerprint follows the hub under the same salt.
	other := Resolve(decode(t, manifest), map[string]string{"acr": values["acr"], "storage": "other"})
	g := f.Take(refs, other)
	if g.String() == f.String() || !bytes.Equal(g.salt, f.salt) {
		t.Errorf("recreated outside Terraform: %q -> %q, salt kept %v", f, g, bytes.Equal(g.salt, f.salt))
	}
	if g.Matches(refs, values) || !g.Matches(refs, map[string]string{"acr": values["acr"], "storage": "other"}) {
		t.Error("the fingerprint must reflect the hub's values")
	}
	// A vanished path leaves the fingerprint of the rest; the configured
	// values no longer match.
	deleteAt(t, other, twinpatch.Path{"$edgeAgent", "properties.desired", "modules"})
	if h := f.Take(refs, other); h.String() == "" || h.Matches(refs, values) {
		t.Errorf("vanished path: %q", h)
	}
	if f.Take(nil, other).String() != "" {
		t.Error("no placeholders, no fingerprint")
	}
}

func TestFingerprint_SaltsDiffer(t *testing.T) {
	refs := Refs(decode(t, manifest))
	hub := Resolve(decode(t, manifest), values)
	a, b := Fingerprint{}.Take(refs, hub), Fingerprint{}.Take(refs, hub)
	if bytes.Equal(a.salt, b.salt) || bytes.Equal(a.hash, b.hash) {
		t.Error("two resources must not share a salt")
	}
}

func TestDocPathKey(t *testing.T) {
	refs := Refs(decode(t, manifest))
	if len(refs) == 0 || !strings.HasPrefix(refs[0].segs.key(), `["$edgeAgent","properties.desired",`) {
		t.Errorf("key: %v", refs[0].segs.key())
	}
	a := Refs(decode(t, `{"a.b":"{{secret.x}}"}`))
	b := Refs(decode(t, `{"a":{"b":"{{secret.x}}"}}`))
	if len(a) != 1 || len(b) != 1 || a[0].Path != b[0].Path || a[0].segs.key() == b[0].segs.key() {
		t.Errorf("dotted and nested keys: paths %q %q, keys %q %q", a[0].Path, b[0].Path, a[0].segs.key(), b[0].segs.key())
	}
}

// TestFingerprint_EmptyValueAndNoStrings: an empty value is a value the
// fingerprint tells from another; a hub without a string at any
// placeholder path has no fingerprint, which matches nothing — the next
// plan replaces.
func TestFingerprint_EmptyValueAndNoStrings(t *testing.T) {
	refs := Refs(decode(t, manifest))
	empty := map[string]string{"acr": "", "storage": values["storage"]}
	f := Fingerprint{}.Take(refs, Resolve(decode(t, manifest), empty))
	if f.String() == "" || !f.Matches(refs, empty) || f.Matches(refs, values) {
		t.Errorf("empty value: %q", f)
	}
	hub := Resolve(decode(t, manifest), values)
	twinpatch.Set(hub, twinpatch.Path{"$edgeAgent", "properties.desired", "runtime", "settings", "registryCredentials", "acr", "password"}, map[string]any{"vault": "ref"})
	twinpatch.Set(hub, twinpatch.Path{"$edgeAgent", "properties.desired", "modules", "collector", "env", "CONN", "value"}, json.Number("1"))
	twinpatch.Set(hub, twinpatch.Path{"$edgeAgent", "properties.desired", "modules", "collector", "env", "LIST", "value"}, []any{})
	if g := f.Take(refs, hub); g.String() != "" || g.Matches(refs, values) {
		t.Errorf("no string at any placeholder path: %q", g)
	}
	if g := f.Take(refs, map[string]any{}); g.String() != "" {
		t.Errorf("every path vanished: %q", g)
	}
}
