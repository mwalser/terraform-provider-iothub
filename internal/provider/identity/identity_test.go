package identity

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
)

func TestAuthFromHub_KeysInStateSwitch(t *testing.T) {
	am := &client.AuthenticationMechanism{Type: "sas", SymmetricKey: &client.SymmetricKey{PrimaryKey: "p", SecondaryKey: "s"}}
	if a := AuthFromHub(am, true); a.PrimaryKey.ValueString() != "p" || a.Type.ValueString() != "sas" {
		t.Errorf("keys in state: %+v", a)
	}
	if a := AuthFromHub(am, false); !a.PrimaryKey.IsNull() || !a.SecondaryKey.IsNull() {
		t.Errorf("write-only: keys must be null, got %+v", a)
	}
	x := &client.AuthenticationMechanism{Type: "selfSigned", X509Thumbprint: &client.X509Thumbprint{PrimaryThumbprint: "AB"}}
	if a := AuthFromHub(x, true); a.PrimaryThumbprint.ValueString() != "AB" || !a.SecondaryThumbprint.IsNull() || !a.PrimaryKey.IsNull() {
		t.Errorf("x509: %+v", a)
	}
	if a := AuthFromHub(nil, true); !a.Type.IsNull() {
		t.Errorf("nil: %+v", a)
	}
}

func TestDiffAuth(t *testing.T) {
	prior := WrittenAuth{Type: "sas", PrimaryKey: "k1", SecondaryKey: "k2"}
	if d := DiffAuth(prior, prior); len(d) != 0 {
		t.Errorf("identical: %v", d)
	}
	// Key rotation is reported without echoing keys.
	fresh := prior
	fresh.PrimaryKey = "rotated"
	if d := DiffAuth(prior, fresh); len(d) != 1 || d[0] != "authentication.primary_key: (rotated)" {
		t.Errorf("key diff: %v", d)
	}
	// Unknown prior keys (write-only) are not compared.
	prior2 := prior
	prior2.PrimaryKey, prior2.SecondaryKey = "", ""
	if d := DiffAuth(prior2, fresh); len(d) != 0 {
		t.Errorf("write-only keys must not be compared: %v", d)
	}
	// Thumbprints compare case-insensitively.
	a := WrittenAuth{Type: "selfSigned", PrimaryThumbprint: "abcdef"}
	b := WrittenAuth{Type: "selfSigned", PrimaryThumbprint: "ABCDEF"}
	if d := DiffAuth(a, b); len(d) != 0 {
		t.Errorf("thumbprint case: %v", d)
	}
	if d := DiffAuth(a, WrittenAuth{Type: "certificateAuthority"}); len(d) != 2 {
		t.Errorf("type + thumbprint: %v", d)
	}
}

func TestChooseKey(t *testing.T) {
	if ChooseKey(types.StringValue("cfg"), types.StringValue("wo"), "cur") != "cfg" {
		t.Error("explicit config wins")
	}
	if ChooseKey(types.StringNull(), types.StringValue("wo"), "cur") != "wo" {
		t.Error("write-only next")
	}
	if ChooseKey(types.StringNull(), types.StringNull(), "cur") != "cur" {
		t.Error("current hub value last")
	}
	if ChooseKey(types.StringUnknown(), types.StringNull(), "") != "" {
		t.Error("nothing known -> hub generates")
	}
}

func TestPatterns(t *testing.T) {
	for _, ok := range []string{"a", "dev-01", "s:1.2+x%_#*?!(),=@;$'", "A", "$edgeAgent"} {
		if !IDPattern.MatchString(ok) {
			t.Errorf("%q should be a valid id", ok)
		}
	}
	for _, bad := range []string{"", "with space", "slash/x", "ü", "x\\y"} {
		if IDPattern.MatchString(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
	if !ThumbprintPattern.MatchString("aabbccddeeff00112233445566778899aabbccdd") || ThumbprintPattern.MatchString("aa:bb") || ThumbprintPattern.MatchString("abc") {
		t.Error("thumbprint pattern")
	}
}

func obj(a Auth) types.Object { return a.Object() }

func nulls() Auth {
	return Auth{Type: types.StringNull(), PrimaryKey: types.StringNull(), SecondaryKey: types.StringNull(), PrimaryThumbprint: types.StringNull(), SecondaryThumbprint: types.StringNull()}
}

func TestPlanAuth(t *testing.T) {
	ctx := context.Background()
	nullObj := types.ObjectNull(AuthAttrTypes)

	// omitted, no state -> unknown (hub generates)
	got, _ := PlanAuth(ctx, nullObj, nullObj, WriteOnlyKeys{})
	if !got.IsUnknown() {
		t.Errorf("create with omitted block must be unknown, got %v", got)
	}
	// omitted, state present -> state
	st := nulls()
	st.Type, st.PrimaryKey, st.SecondaryKey = types.StringValue("sas"), types.StringValue("p"), types.StringValue("s")
	got, _ = PlanAuth(ctx, nullObj, obj(st), WriteOnlyKeys{})
	if !got.Equal(obj(st)) {
		t.Errorf("omitted block keeps state, got %v", got)
	}
	// sas with no keys, state sas -> keys from state (no perpetual diff)
	cfg := nulls()
	cfg.Type = types.StringValue("sas")
	got, _ = PlanAuth(ctx, obj(cfg), obj(st), WriteOnlyKeys{})
	if !got.Equal(obj(st)) {
		t.Errorf("sas without keys keeps state keys, got %v", got)
	}
	// sas, write-only primary -> primary null, secondary from state
	got, _ = PlanAuth(ctx, obj(cfg), obj(st), WriteOnlyKeys{PrimaryVersion: types.Int64Value(1)})
	want := st
	want.PrimaryKey = types.StringNull()
	if !got.Equal(obj(want)) {
		t.Errorf("write-only primary: got %v", got)
	}
	// sas from x509 state -> keys unknown
	x := nulls()
	x.Type, x.PrimaryThumbprint = types.StringValue("selfSigned"), types.StringValue("AA")
	got, _ = PlanAuth(ctx, obj(cfg), obj(x), WriteOnlyKeys{})
	var a Auth
	_ = got.As(ctx, &a, objectAsOptions)
	if !a.PrimaryKey.IsUnknown() || !a.SecondaryKey.IsUnknown() || !a.PrimaryThumbprint.IsNull() {
		t.Errorf("sas from x509: %+v", a)
	}
	// selfSigned -> keys null, thumbprints from config
	cfg = nulls()
	cfg.Type, cfg.PrimaryThumbprint = types.StringValue("selfSigned"), types.StringValue("BB")
	got, _ = PlanAuth(ctx, obj(cfg), obj(st), WriteOnlyKeys{})
	_ = got.As(ctx, &a, objectAsOptions)
	if !a.PrimaryKey.IsNull() || a.PrimaryThumbprint.ValueString() != "BB" || !a.SecondaryThumbprint.IsNull() {
		t.Errorf("selfSigned: %+v", a)
	}
}

func TestBuildAuth(t *testing.T) {
	ctx := context.Background()
	// unknown block on create -> sas, hub generates
	am, diags := BuildAuth(ctx, types.ObjectUnknown(AuthAttrTypes), WriteOnlyKeys{}, nil)
	if diags.HasError() || am.Type != "sas" || am.SymmetricKey != nil {
		t.Errorf("create default: %+v %v", am, diags)
	}
	// unknown block on update -> keep current
	cur := &client.AuthenticationMechanism{Type: "certificateAuthority"}
	am, _ = BuildAuth(ctx, types.ObjectUnknown(AuthAttrTypes), WriteOnlyKeys{}, cur)
	if am.Type != "certificateAuthority" {
		t.Errorf("keep current: %+v", am)
	}
	// only one key given -> counterpart generated
	a := nulls()
	a.Type, a.PrimaryKey = types.StringValue("sas"), types.StringValue("cGtleQ==")
	am, _ = BuildAuth(ctx, a.Object(), WriteOnlyKeys{}, nil)
	if am.SymmetricKey == nil || am.SymmetricKey.PrimaryKey != "cGtleQ==" || am.SymmetricKey.SecondaryKey == "" {
		t.Errorf("counterpart generation: %+v", am.SymmetricKey)
	}
	// write-only primary + current secondary
	a.PrimaryKey = types.StringNull()
	curSAS := &client.AuthenticationMechanism{Type: "sas", SymmetricKey: &client.SymmetricKey{PrimaryKey: "old-p", SecondaryKey: "old-s"}}
	am, _ = BuildAuth(ctx, a.Object(), WriteOnlyKeys{Primary: types.StringValue("new-p")}, curSAS)
	if am.SymmetricKey.PrimaryKey != "new-p" || am.SymmetricKey.SecondaryKey != "old-s" {
		t.Errorf("write-only + current: %+v", am.SymmetricKey)
	}
	// selfSigned
	x := nulls()
	x.Type, x.PrimaryThumbprint = types.StringValue("selfSigned"), types.StringValue("AA")
	am, _ = BuildAuth(ctx, x.Object(), WriteOnlyKeys{}, curSAS)
	if am.Type != "selfSigned" || am.SymmetricKey != nil || am.X509Thumbprint.PrimaryThumbprint != "AA" {
		t.Errorf("selfSigned: %+v", am)
	}
}

func TestValidateAuth(t *testing.T) {
	ctx := context.Background()
	a := nulls()
	a.Type, a.PrimaryThumbprint = types.StringValue("sas"), types.StringValue("AA")
	if d := ValidateAuth(ctx, a.Object(), WriteOnlyKeys{}); !d.HasError() {
		t.Error("sas + thumbprint must fail")
	}
	a = nulls()
	a.Type = types.StringValue("selfSigned")
	if d := ValidateAuth(ctx, a.Object(), WriteOnlyKeys{}); !d.HasError() {
		t.Error("selfSigned without thumbprint must fail")
	}
	a.PrimaryThumbprint = types.StringValue("AA")
	if d := ValidateAuth(ctx, a.Object(), WriteOnlyKeys{Primary: types.StringValue("k")}); !d.HasError() {
		t.Error("selfSigned + write-only key must fail")
	}
	if d := ValidateAuth(ctx, a.Object(), WriteOnlyKeys{}); d.HasError() {
		t.Errorf("valid selfSigned: %v", d)
	}
	a = nulls()
	a.Type, a.PrimaryKey = types.StringValue("certificateAuthority"), types.StringValue("k")
	if d := ValidateAuth(ctx, a.Object(), WriteOnlyKeys{}); !d.HasError() {
		t.Error("certificateAuthority + key must fail")
	}
	if d := ValidateAuth(ctx, types.ObjectNull(AuthAttrTypes), WriteOnlyKeys{}); d.HasError() {
		t.Error("omitted block is fine")
	}
}
