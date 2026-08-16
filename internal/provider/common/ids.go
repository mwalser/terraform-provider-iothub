package common

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
)

// ModuleID builds the ID of a module: <deviceId>/<moduleId>. Device IDs
// cannot contain a slash, so the form is unambiguous.
func ModuleID(deviceID, moduleID string) string { return deviceID + "/" + moduleID }

// ParseModuleID splits an ID produced by ModuleID.
func ParseModuleID(id string) (deviceID, moduleID string, err error) {
	id = strings.TrimSpace(id)
	deviceID, moduleID, ok := strings.Cut(id, "/")
	if !ok || deviceID == "" || moduleID == "" || strings.Contains(moduleID, "/") {
		return "", "", fmt.Errorf("expected an ID of the form <device_id>/<module_id>, got %q", id)
	}
	return deviceID, moduleID, nil
}

// NewSymmetricKey returns a fresh base64 32-byte key, the same size the hub
// generates.
func NewSymmetricKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(b), nil
}
