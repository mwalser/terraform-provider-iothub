package client

import (
	"context"
	"net/http"
)

// RegistryStatistics is GET /statistics/devices.
type RegistryStatistics struct {
	TotalDeviceCount    int64 `json:"totalDeviceCount"`
	EnabledDeviceCount  int64 `json:"enabledDeviceCount"`
	DisabledDeviceCount int64 `json:"disabledDeviceCount"`
}

// ServiceStatistics is GET /statistics/service.
type ServiceStatistics struct {
	ConnectedDeviceCount int64 `json:"connectedDeviceCount"`
}

// GetRegistryStatistics returns identity-registry counts. The counts lag
// behind registry changes by a while (CONCEPT.md §11.5).
func (c *Client) GetRegistryStatistics(ctx context.Context) (*RegistryStatistics, error) {
	var out RegistryStatistics
	if _, err := c.do(ctx, request{method: http.MethodGet, path: "/statistics/devices"}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetServiceStatistics returns the connected-device count (approximate).
func (c *Client) GetServiceStatistics(ctx context.Context) (*ServiceStatistics, error) {
	var out ServiceStatistics
	if _, err := c.do(ctx, request{method: http.MethodGet, path: "/statistics/service"}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
