package client

import (
	"context"
	"fmt"
	"net/http"
)

// Module is a module identity as returned by the service. Modules have no
// status of their own; a disabled device disables its modules.
type Module struct {
	ModuleID                   string                   `json:"moduleId"`
	DeviceID                   string                   `json:"deviceId"`
	ManagedBy                  string                   `json:"managedBy,omitempty"`
	GenerationID               string                   `json:"generationId,omitempty"`
	ETag                       string                   `json:"etag,omitempty"`
	ConnectionState            string                   `json:"connectionState,omitempty"`
	ConnectionStateUpdatedTime string                   `json:"connectionStateUpdatedTime,omitempty"`
	LastActivityTime           string                   `json:"lastActivityTime,omitempty"`
	CloudToDeviceMessageCount  int64                    `json:"cloudToDeviceMessageCount,omitempty"`
	Authentication             *AuthenticationMechanism `json:"authentication,omitempty"`
}

// ModuleSpec is what the provider writes. Like devices, PUT is a full
// replace: an omitted managedBy clears it and an omitted authentication makes
// the hub mint new keys (verified), so every field is sent on every write.
type ModuleSpec struct {
	DeviceID       string
	ModuleID       string
	ManagedBy      string
	Authentication AuthenticationMechanism
}

func (s ModuleSpec) body() map[string]any {
	b := map[string]any{
		"deviceId":       s.DeviceID,
		"moduleId":       s.ModuleID,
		"authentication": s.Authentication,
	}
	if s.ManagedBy != "" {
		b["managedBy"] = s.ManagedBy
	} else {
		b["managedBy"] = nil
	}
	return b
}

func modulePath(deviceID, moduleID string) string {
	return devicePath(deviceID) + "/modules/" + moduleID
}

// GetModule reads a module identity. A missing device or module yields
// IsNotFound.
func (c *Client) GetModule(ctx context.Context, deviceID, moduleID string) (*Module, error) {
	var out Module
	if _, err := c.do(ctx, request{method: http.MethodGet, path: modulePath(deviceID, moduleID)}, &out); err != nil {
		return nil, c.moduleError(ctx, deviceID, err)
	}
	return &out, nil
}

// ListModules returns all module identities of a device (GET
// /devices/{id}/modules). A missing device yields IsNotFound.
func (c *Client) ListModules(ctx context.Context, deviceID string) ([]Module, error) {
	var out []Module
	if _, err := c.do(ctx, request{method: http.MethodGet, path: devicePath(deviceID) + "/modules"}, &out); err != nil {
		return nil, c.moduleError(ctx, deviceID, err)
	}
	return out, nil
}

// CreateModule registers a module identity on an existing device. A PUT
// without If-Match is create-only: an existing module answers 409
// (IsConflict, ModuleAlreadyExistsOnDevice); a missing device 404.
func (c *Client) CreateModule(ctx context.Context, spec ModuleSpec) (*Module, error) {
	var out Module
	if _, err := c.do(ctx, request{method: http.MethodPut, path: modulePath(spec.DeviceID, spec.ModuleID), body: spec.body()}, &out); err != nil {
		return nil, c.moduleError(ctx, spec.DeviceID, err)
	}
	return &out, nil
}

// UpdateModule replaces a module identity; see UpdateDevice for the etag
// contract.
func (c *Client) UpdateModule(ctx context.Context, spec ModuleSpec, etag string) (*Module, error) {
	var out Module
	r := request{method: http.MethodPut, path: modulePath(spec.DeviceID, spec.ModuleID), body: spec.body(), headers: ifMatch(etag)}
	if _, err := c.do(ctx, r, &out); err != nil {
		return nil, c.moduleError(ctx, spec.DeviceID, err)
	}
	return &out, nil
}

// DeleteModule removes a module identity and its twin. If-Match is required
// by the service ("*" deletes unconditionally); a missing module answers 404.
func (c *Client) DeleteModule(ctx context.Context, deviceID, moduleID, etag string) error {
	if etag == "" {
		etag = "*"
	}
	_, err := c.do(ctx, request{method: http.MethodDelete, path: modulePath(deviceID, moduleID), headers: ifMatch(etag)}, nil)
	return c.moduleError(ctx, deviceID, err)
}

// moduleError normalises a service quirk: under shared-access-signature
// authentication, module identity operations on a device that does not
// exist answer 401 IotHubUnauthorizedAccess instead of 404 (Entra ID gets
// 404; verified). A real authorization failure also fails the device read,
// so the device is probed once and a missing device becomes IsNotFound.
func (c *Client) moduleError(ctx context.Context, deviceID string, err error) error {
	if err == nil || !IsUnauthorized(err) {
		return err
	}
	if _, derr := c.GetDevice(ctx, deviceID); IsNotFound(derr) {
		e, _ := AsError(err)
		return &Error{
			StatusCode: http.StatusNotFound,
			Code:       "DeviceNotFound",
			Message:    fmt.Sprintf("device %q does not exist (the service answers module operations on a missing device with 401 under SAS authentication)", deviceID),
			TrackingID: e.TrackingID,
			RequestID:  e.RequestID,
			Method:     e.Method,
			URL:        e.URL,
			Body:       e.Body,
		}
	}
	return err
}

// ModuleConnectionString renders a module connection string.
func ModuleConnectionString(hostname, deviceID, moduleID, key string) string {
	return "HostName=" + hostname + ";DeviceId=" + deviceID + ";ModuleId=" + moduleID + ";SharedAccessKey=" + key
}
