package common

import "testing"

func TestResourceID_RoundTrip(t *testing.T) {
	id := ResourceID("Hub.azure-devices.net", "devices", "dev-1")
	if id != "hub.azure-devices.net/devices/dev-1" {
		t.Fatalf("id = %q", id)
	}
	host, parts, err := ParseResourceID(id, "devices")
	if err != nil || host != "hub.azure-devices.net" || len(parts) != 1 || parts[0] != "dev-1" {
		t.Fatalf("parse: %q %v %v", host, parts, err)
	}
	host, parts, err = ParseResourceID("hub.azure-devices.net/devices/d/modules/m", "devices")
	if err != nil || host != "hub.azure-devices.net" || len(parts) != 3 || parts[2] != "m" {
		t.Fatalf("parse module: %q %v %v", host, parts, err)
	}
	for _, bad := range []string{"", "hub", "hub/", "hub/twins/x", "hub/devices/", "/devices/x"} {
		if _, _, err := ParseResourceID(bad, "devices"); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestNewSymmetricKey(t *testing.T) {
	k1, err := NewSymmetricKey()
	k2, _ := NewSymmetricKey()
	if err != nil || len(k1) != 44 || k1 == k2 {
		t.Fatalf("keys: %q %q %v", k1, k2, err)
	}
}
