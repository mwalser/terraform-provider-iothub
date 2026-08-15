package client

import (
	"context"
	"net/http"
	"strings"
)

// Authentication types of the identity registry (API literals).
const (
	AuthTypeSAS                  = "sas"
	AuthTypeSelfSigned           = "selfSigned"
	AuthTypeCertificateAuthority = "certificateAuthority"
	AuthTypeNone                 = "none"
)

// Device statuses (API literals).
const (
	StatusEnabled  = "enabled"
	StatusDisabled = "disabled"
)

// Device is an identity-registry entry as returned by the service.
type Device struct {
	DeviceID                   string                   `json:"deviceId"`
	GenerationID               string                   `json:"generationId,omitempty"`
	ETag                       string                   `json:"etag,omitempty"`
	ConnectionState            string                   `json:"connectionState,omitempty"`
	Status                     string                   `json:"status,omitempty"`
	StatusReason               *string                  `json:"statusReason,omitempty"`
	ConnectionStateUpdatedTime string                   `json:"connectionStateUpdatedTime,omitempty"`
	StatusUpdatedTime          string                   `json:"statusUpdatedTime,omitempty"`
	LastActivityTime           string                   `json:"lastActivityTime,omitempty"`
	CloudToDeviceMessageCount  int64                    `json:"cloudToDeviceMessageCount,omitempty"`
	Authentication             *AuthenticationMechanism `json:"authentication,omitempty"`
	Capabilities               *DeviceCapabilities      `json:"capabilities,omitempty"`
	DeviceScope                string                   `json:"deviceScope,omitempty"`
	ParentScopes               []string                 `json:"parentScopes,omitempty"`
}

// AuthenticationMechanism describes how a device or module authenticates.
type AuthenticationMechanism struct {
	Type           string          `json:"type"`
	SymmetricKey   *SymmetricKey   `json:"symmetricKey,omitempty"`
	X509Thumbprint *X509Thumbprint `json:"x509Thumbprint,omitempty"`
}

// SymmetricKey holds base64 keys; the service requires both or neither.
type SymmetricKey struct {
	PrimaryKey   string `json:"primaryKey,omitempty"`
	SecondaryKey string `json:"secondaryKey,omitempty"`
}

// X509Thumbprint holds hex thumbprints (no separators; case preserved by the
// service).
type X509Thumbprint struct {
	PrimaryThumbprint   string `json:"primaryThumbprint,omitempty"`
	SecondaryThumbprint string `json:"secondaryThumbprint,omitempty"`
}

// DeviceCapabilities flags an identity as IoT Edge.
type DeviceCapabilities struct {
	IotEdge bool `json:"iotEdge"`
}

// DeviceSpec is what the provider writes. PUT /devices/{id} is a full
// replace (omitting authentication makes the hub mint new keys), so every
// field is sent on every write. Scope handling follows the service rules
// verified in CONCEPT.md Appendix D:
//   - edge devices must echo their own, hub-generated deviceScope on updates
//     (omitting it is "Device scope is immutable") and carry the parent in
//     parentScopes (an explicit [] detaches);
//   - leaf devices carry the parent in deviceScope ("" detaches);
//   - a leaf becoming an edge device sends deviceScope "" (the hub then
//     generates one), optionally with parentScopes to keep its parent.
type DeviceSpec struct {
	DeviceID       string
	Status         string
	StatusReason   string
	Authentication AuthenticationMechanism
	IotEdge        bool
	// ParentScope is the deviceScope of the parent edge device, if any.
	ParentScope string
	// OwnDeviceScope is the device's current hub-generated scope; required
	// when updating an edge device, empty on create or when a leaf device is
	// being turned into an edge device.
	OwnDeviceScope string
}

// body renders the wire representation of the spec.
func (s DeviceSpec) body() map[string]any {
	b := map[string]any{
		"deviceId":       s.DeviceID,
		"status":         s.Status,
		"authentication": s.Authentication,
		"capabilities":   DeviceCapabilities{IotEdge: s.IotEdge},
	}
	if s.StatusReason != "" {
		b["statusReason"] = s.StatusReason
	} else {
		b["statusReason"] = nil
	}
	if s.IotEdge {
		if s.OwnDeviceScope != "" {
			b["deviceScope"] = s.OwnDeviceScope
		}
		scopes := []string{}
		if s.ParentScope != "" {
			scopes = append(scopes, s.ParentScope)
		}
		b["parentScopes"] = scopes // [] clears a former parent
	} else {
		b["deviceScope"] = s.ParentScope // "" clears a former parent
	}
	return b
}

func devicePath(id string) string { return "/devices/" + id }

// GetDevice reads an identity. Missing devices yield IsNotFound.
func (c *Client) GetDevice(ctx context.Context, id string) (*Device, error) {
	var out Device
	if _, err := c.do(ctx, request{method: http.MethodGet, path: devicePath(id)}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateDevice registers a new identity. The service treats a PUT without
// If-Match as create-only: an existing ID answers 409 (IsConflict).
func (c *Client) CreateDevice(ctx context.Context, spec DeviceSpec) (*Device, error) {
	var out Device
	if _, err := c.do(ctx, request{method: http.MethodPut, path: devicePath(spec.DeviceID), body: spec.body()}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateDevice replaces an identity. etag is the value from the last read
// (quoted for the wire) or "*" to skip the check; a stale value answers 412
// (IsPreconditionFailed) and a missing device 404.
func (c *Client) UpdateDevice(ctx context.Context, spec DeviceSpec, etag string) (*Device, error) {
	var out Device
	r := request{method: http.MethodPut, path: devicePath(spec.DeviceID), body: spec.body(), headers: ifMatch(etag)}
	if _, err := c.do(ctx, r, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteDevice removes an identity, its twin and its modules. If-Match is
// required by the service; "*" deletes unconditionally. A missing device
// answers 404 (IsNotFound), which callers usually treat as success.
func (c *Client) DeleteDevice(ctx context.Context, id string, etag string) error {
	if etag == "" {
		etag = "*"
	}
	_, err := c.do(ctx, request{method: http.MethodDelete, path: devicePath(id), headers: ifMatch(etag)}, nil)
	return err
}

// ifMatch builds the If-Match header. Identity ETags are accepted quoted or
// unquoted, configuration ETags only quoted — so everything is quoted.
func ifMatch(etag string) http.Header {
	return http.Header{"If-Match": []string{QuoteETag(etag)}}
}

// QuoteETag returns etag in the form the service expects in If-Match.
func QuoteETag(etag string) string {
	etag = strings.TrimSpace(etag)
	if etag == "" || etag == "*" || strings.HasPrefix(etag, `"`) {
		return etag
	}
	return `"` + etag + `"`
}

// DeviceConnectionString renders a device connection string.
func DeviceConnectionString(hostname, deviceID, key string) string {
	return "HostName=" + hostname + ";DeviceId=" + deviceID + ";SharedAccessKey=" + key
}
