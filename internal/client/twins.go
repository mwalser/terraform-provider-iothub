package client

import (
	"context"
	"encoding/json"
	"net/http"
)

// Twin is a device or module twin as returned by GET/PATCH /twins/…. The
// tags and property sections are kept raw: the provider decodes them with
// number-preserving semantics (package twinpatch). The identity mirror
// (status, connection state, …) is included for the data sources.
type Twin struct {
	DeviceID                  string          `json:"deviceId"`
	ModuleID                  string          `json:"moduleId,omitempty"`
	ETag                      string          `json:"etag,omitempty"`
	DeviceETag                string          `json:"deviceEtag,omitempty"`
	Version                   int64           `json:"version,omitempty"`
	Tags                      json.RawMessage `json:"tags,omitempty"`
	Properties                TwinProperties  `json:"properties"`
	ModelID                   string          `json:"modelId,omitempty"`
	Status                    string          `json:"status,omitempty"`
	StatusReason              *string         `json:"statusReason,omitempty"`
	StatusUpdateTime          string          `json:"statusUpdateTime,omitempty"`
	ConnectionState           string          `json:"connectionState,omitempty"`
	LastActivityTime          string          `json:"lastActivityTime,omitempty"`
	CloudToDeviceMessageCount int64           `json:"cloudToDeviceMessageCount,omitempty"`
	AuthenticationType        string          `json:"authenticationType,omitempty"`
	// X509Thumbprint decodes the twin's capitalised `PrimaryThumbprint` too
	// (encoding/json matches keys case-insensitively).
	X509Thumbprint *X509Thumbprint     `json:"x509Thumbprint,omitempty"`
	Capabilities   *DeviceCapabilities `json:"capabilities,omitempty"`
	DeviceScope    string              `json:"deviceScope,omitempty"`
	ParentScopes   []string            `json:"parentScopes,omitempty"`
}

// TwinProperties are the desired and reported sections, raw.
type TwinProperties struct {
	Desired  json.RawMessage `json:"desired,omitempty"`
	Reported json.RawMessage `json:"reported,omitempty"`
}

// TwinPatch is a JSON merge patch for a twin (RFC 7386, verified live):
// keys are merged recursively, a null removes a key, arrays are replaced.
// A nil section is left out of the request.
type TwinPatch struct {
	Tags    map[string]any
	Desired map[string]any
}

// IsEmpty reports whether the patch would send nothing.
func (p TwinPatch) IsEmpty() bool { return p.Tags == nil && p.Desired == nil }

func (p TwinPatch) body() map[string]any {
	b := map[string]any{}
	if p.Tags != nil {
		b["tags"] = p.Tags
	}
	if p.Desired != nil {
		b["properties"] = map[string]any{"desired": p.Desired}
	}
	return b
}

func deviceTwinPath(deviceID string) string { return "/twins/" + deviceID }
func moduleTwinPath(deviceID, moduleID string) string {
	return "/twins/" + deviceID + "/modules/" + moduleID
}

// GetDeviceTwin reads a device twin; a missing device yields IsNotFound.
func (c *Client) GetDeviceTwin(ctx context.Context, deviceID string) (*Twin, error) {
	return c.getTwin(ctx, deviceTwinPath(deviceID))
}

// GetModuleTwin reads a module twin. A missing module answers 404 with
// DeviceNotFound (verified), i.e. IsNotFound either way.
func (c *Client) GetModuleTwin(ctx context.Context, deviceID, moduleID string) (*Twin, error) {
	return c.getTwin(ctx, moduleTwinPath(deviceID, moduleID))
}

// PatchDeviceTwin merge-patches tags and/or desired properties. etag "" or
// "*" skips the precondition (the provider's choice for twins, CONCEPT.md
// §11.1); a stale value answers 412. Returns the updated twin.
func (c *Client) PatchDeviceTwin(ctx context.Context, deviceID string, patch TwinPatch, etag string) (*Twin, error) {
	return c.patchTwin(ctx, deviceTwinPath(deviceID), patch, etag)
}

// PatchModuleTwin is PatchDeviceTwin for a module twin.
func (c *Client) PatchModuleTwin(ctx context.Context, deviceID, moduleID string, patch TwinPatch, etag string) (*Twin, error) {
	return c.patchTwin(ctx, moduleTwinPath(deviceID, moduleID), patch, etag)
}

func (c *Client) getTwin(ctx context.Context, path string) (*Twin, error) {
	var out Twin
	if _, err := c.do(ctx, request{method: http.MethodGet, path: path}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) patchTwin(ctx context.Context, path string, patch TwinPatch, etag string) (*Twin, error) {
	if etag == "" {
		etag = "*"
	}
	var out Twin
	r := request{method: http.MethodPatch, path: path, body: patch.body(), headers: ifMatch(etag)}
	if _, err := c.do(ctx, r, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
