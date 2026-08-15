package common

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
)

// ResourceID builds the provider's resource ID / import ID, which mirrors the
// REST path: <hostname>/devices/<deviceId>[/modules/<moduleId>], <hostname>/twins/…,
// <hostname>/configurations/<id>.
func ResourceID(hostname string, segments ...string) string {
	return strings.ToLower(hostname) + "/" + strings.Join(segments, "/")
}

// ParseResourceID splits an ID produced by ResourceID. kind is the first path
// segment expected after the hostname ("devices", "twins", "configurations");
// the returned parts are the remaining segments.
func ParseResourceID(id, kind string) (hostname string, parts []string, err error) {
	id = strings.TrimSpace(id)
	host, rest, ok := strings.Cut(id, "/")
	if !ok || host == "" || rest == "" {
		return "", nil, fmt.Errorf("expected an ID of the form <hostname>/%s/<id>, got %q", kind, id)
	}
	segs := strings.Split(rest, "/")
	if segs[0] != kind || len(segs) < 2 || segs[1] == "" {
		return "", nil, fmt.Errorf("expected an ID of the form <hostname>/%s/<id>, got %q", kind, id)
	}
	return host, segs[1:], nil
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
