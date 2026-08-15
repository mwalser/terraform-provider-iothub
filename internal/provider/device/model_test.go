package device

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
)

func TestParentScopeOf(t *testing.T) {
	leaf := &client.Device{DeviceScope: "ms-azure-iot-edge://p-1", ParentScopes: []string{"ms-azure-iot-edge://p-1"}}
	if parentScopeOf(leaf) != "ms-azure-iot-edge://p-1" {
		t.Error("leaf: deviceScope is the parent")
	}
	edge := &client.Device{Capabilities: &client.DeviceCapabilities{IotEdge: true}, DeviceScope: "ms-azure-iot-edge://self-9", ParentScopes: []string{"ms-azure-iot-edge://p-1"}}
	if parentScopeOf(edge) != "ms-azure-iot-edge://p-1" {
		t.Error("edge: parentScopes[0] is the parent, deviceScope is its own")
	}
	if parentScopeOf(&client.Device{Capabilities: &client.DeviceCapabilities{IotEdge: true}, DeviceScope: "ms-azure-iot-edge://self-9"}) != "" {
		t.Error("edge without parent")
	}
}

func TestAuthFromHub_KeysInStateSwitch(t *testing.T) {
	am := &client.AuthenticationMechanism{Type: "sas", SymmetricKey: &client.SymmetricKey{PrimaryKey: "p", SecondaryKey: "s"}}
	if a := authFromHub(am, true); a.PrimaryKey.ValueString() != "p" || a.Type.ValueString() != "sas" {
		t.Errorf("keys in state: %+v", a)
	}
	if a := authFromHub(am, false); !a.PrimaryKey.IsNull() || !a.SecondaryKey.IsNull() {
		t.Errorf("write-only: keys must be null, got %+v", a)
	}
	x := &client.AuthenticationMechanism{Type: "selfSigned", X509Thumbprint: &client.X509Thumbprint{PrimaryThumbprint: "AB"}}
	if a := authFromHub(x, true); a.PrimaryThumbprint.ValueString() != "AB" || !a.SecondaryThumbprint.IsNull() || !a.PrimaryKey.IsNull() {
		t.Errorf("x509: %+v", a)
	}
}

func TestDiffWritten(t *testing.T) {
	prior := writtenFields{Status: "enabled", IotEdge: false, ParentScope: "", AuthType: "sas", PrimaryKey: "k1", SecondaryKey: "k2"}
	// Volatile changes are invisible: only written fields count.
	if d := diffWritten(prior, prior); len(d) != 0 {
		t.Errorf("identical: %v", d)
	}
	fresh := prior
	fresh.Status, fresh.StatusReason = "disabled", "compromised"
	d := diffWritten(prior, fresh)
	if len(d) != 2 || d[0] != `status: "enabled" → "disabled"` || d[1] != `status_reason: "" → "compromised"` {
		t.Errorf("status diff: %v", d)
	}
	// Key rotation is reported without echoing keys.
	fresh = prior
	fresh.PrimaryKey = "rotated"
	if d := diffWritten(prior, fresh); len(d) != 1 || d[0] != "authentication.primary_key: (rotated)" {
		t.Errorf("key diff: %v", d)
	}
	// Unknown prior keys (write-only) are not compared.
	prior2 := prior
	prior2.PrimaryKey, prior2.SecondaryKey = "", ""
	if d := diffWritten(prior2, fresh); len(d) != 0 {
		t.Errorf("write-only keys must not be compared: %v", d)
	}
	// Thumbprints compare case-insensitively.
	a := writtenFields{AuthType: "selfSigned", PrimaryThumbprint: "abcdef"}
	b := writtenFields{AuthType: "selfSigned", PrimaryThumbprint: "ABCDEF"}
	if d := diffWritten(a, b); len(d) != 0 {
		t.Errorf("thumbprint case: %v", d)
	}
}

func TestChooseKey(t *testing.T) {
	if chooseKey(types.StringValue("cfg"), types.StringValue("wo"), "cur") != "cfg" {
		t.Error("explicit config wins")
	}
	if chooseKey(types.StringNull(), types.StringValue("wo"), "cur") != "wo" {
		t.Error("write-only next")
	}
	if chooseKey(types.StringNull(), types.StringNull(), "cur") != "cur" {
		t.Error("current hub value last")
	}
	if chooseKey(types.StringUnknown(), types.StringNull(), "") != "" {
		t.Error("nothing known -> hub generates")
	}
}

func TestPatterns(t *testing.T) {
	for _, ok := range []string{"a", "dev-01", "s:1.2+x%_#*?!(),=@;$'", "A"} {
		if !deviceIDPattern.MatchString(ok) {
			t.Errorf("%q should be a valid device id", ok)
		}
	}
	for _, bad := range []string{"", "with space", "slash/x", "ü", "x\\y"} {
		if deviceIDPattern.MatchString(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
	if !thumbprintPattern.MatchString("aabbccddeeff00112233445566778899aabbccdd") || thumbprintPattern.MatchString("aa:bb") || thumbprintPattern.MatchString("abc") {
		t.Error("thumbprint pattern")
	}
}
