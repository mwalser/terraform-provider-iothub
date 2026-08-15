package device

import (
	"testing"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/identity"
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

func TestDiffWritten(t *testing.T) {
	prior := writtenFields{Status: "enabled", Auth: identity.WrittenAuth{Type: "sas", PrimaryKey: "k1", SecondaryKey: "k2"}}
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
	fresh = prior
	fresh.IotEdge, fresh.ParentScope = true, "ms-azure-iot-edge://p-1"
	fresh.Auth.PrimaryKey = "rotated"
	d = diffWritten(prior, fresh)
	if len(d) != 3 || d[0] != "edge_enabled: false → true" || d[1] != `parent_scope: "" → "ms-azure-iot-edge://p-1"` || d[2] != "authentication.primary_key: (rotated)" {
		t.Errorf("diff: %v", d)
	}
}
