package common

import "testing"

func TestModuleID_RoundTrip(t *testing.T) {
	id := ModuleID("dev-1", "m1")
	if id != "dev-1/m1" {
		t.Fatalf("id = %q", id)
	}
	dev, mod, err := ParseModuleID(" dev-1/m1 ")
	if err != nil || dev != "dev-1" || mod != "m1" {
		t.Fatalf("parse: %q %q %v", dev, mod, err)
	}
	for _, bad := range []string{"", "dev-1", "dev-1/", "/m1", "dev-1/m1/x"} {
		if _, _, err := ParseModuleID(bad); err == nil {
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
