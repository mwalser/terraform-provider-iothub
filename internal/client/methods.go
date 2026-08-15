package client

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// MethodRequest is a direct method invocation.
type MethodRequest struct {
	MethodName string `json:"methodName"`
	// Payload is the JSON payload (any JSON value); nil sends null.
	Payload json.RawMessage `json:"payload"`
	// ResponseTimeoutSeconds is how long the service waits for the device's
	// response (5–300); ConnectTimeoutSeconds how long it waits for an
	// offline device to connect (0–300).
	ResponseTimeoutSeconds int64 `json:"responseTimeoutInSeconds"`
	ConnectTimeoutSeconds  int64 `json:"connectTimeoutInSeconds"`
}

// MethodResult is the device's answer: a device-defined status and payload.
type MethodResult struct {
	Status  int64           `json:"status"`
	Payload json.RawMessage `json:"payload"`
}

// InvokeDeviceMethod calls a direct method on a device (POST
// /twins/{id}/methods). An offline device answers 404 with errorCode 404103
// (IsDeviceNotOnline); a missing device 404 (IsNotFound). The call is never
// retried after an ambiguous failure — the method may have run.
func (c *Client) InvokeDeviceMethod(ctx context.Context, deviceID string, req MethodRequest) (*MethodResult, error) {
	return c.invokeMethod(ctx, deviceTwinPath(deviceID)+"/methods", req)
}

// InvokeModuleMethod calls a direct method on a module.
func (c *Client) InvokeModuleMethod(ctx context.Context, deviceID, moduleID string, req MethodRequest) (*MethodResult, error) {
	return c.invokeMethod(ctx, moduleTwinPath(deviceID, moduleID)+"/methods", req)
}

func (c *Client) invokeMethod(ctx context.Context, path string, req MethodRequest) (*MethodResult, error) {
	if req.Payload == nil {
		req.Payload = json.RawMessage("null")
	}
	var out MethodResult
	r := request{method: http.MethodPost, path: path, body: req, retry: perRequest{
		OnlyThrottleRetries: true,
		TryTimeout:          time.Duration(req.ResponseTimeoutSeconds+req.ConnectTimeoutSeconds)*time.Second + 30*time.Second,
	}}
	if _, err := c.do(ctx, r, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// IsDeviceNotOnline reports whether err is the direct-method 404 for a
// registered but disconnected device (errorCode 404103).
func IsDeviceNotOnline(err error) bool {
	e, ok := AsError(err)
	if !ok || e.StatusCode != http.StatusNotFound {
		return false
	}
	return e.Code == "DeviceNotOnline" || strings.Contains(string(e.Body), "404103")
}

// ApplyConfigurationContent pushes an IoT Edge deployment manifest's
// modulesContent to one edge device immediately (POST
// /devices/{id}/applyConfigurationContent, 204). A non-edge device answers
// 400 "Not an Azure IoT Edge device", invalid content 400
// InvalidConfigurationContent.
func (c *Client) ApplyConfigurationContent(ctx context.Context, deviceID string, modulesContent json.RawMessage) error {
	body := map[string]any{"modulesContent": modulesContent}
	_, err := c.do(ctx, request{method: http.MethodPost, path: devicePath(deviceID) + "/applyConfigurationContent", body: body}, nil)
	return err
}

// PurgeResult is the answer of a cloud-to-device queue purge.
type PurgeResult struct {
	DeviceID            string `json:"deviceId"`
	TotalMessagesPurged int64  `json:"totalMessagesPurged"`
}

// PurgeCloudToDeviceQueue deletes all pending cloud-to-device messages of a
// device (DELETE /devices/{id}/commands). A missing device answers 404.
func (c *Client) PurgeCloudToDeviceQueue(ctx context.Context, deviceID string) (*PurgeResult, error) {
	var out PurgeResult
	if _, err := c.do(ctx, request{method: http.MethodDelete, path: devicePath(deviceID) + "/commands"}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
