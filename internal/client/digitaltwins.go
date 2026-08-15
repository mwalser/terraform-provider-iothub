package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DigitalTwin is the IoT Plug and Play view of a device (GET
// /digitaltwins/{id}): the document the service derives from the device
// twin — `$dtId`, `$metadata.$model`, root properties and components (objects
// with their own `$metadata`). Verified: the endpoint answers for non-PnP
// devices too (`$model` is then ""), and its ETag equals the twin ETag.
type DigitalTwin struct {
	ID      string
	ModelID string
	ETag    string
	// Document is the raw JSON document as returned by the service.
	Document json.RawMessage
}

// digitalTwinEnvelope decodes the fields the client interprets.
type digitalTwinEnvelope struct {
	ID       string `json:"$dtId"`
	Metadata struct {
		Model string `json:"$model"`
	} `json:"$metadata"`
}

// GetDigitalTwin reads a device's digital twin; a missing device answers 404
// DeviceNotFound (IsNotFound). Under SAS the policy needs ServiceConnect
// (verified: registryReadWrite → 401).
func (c *Client) GetDigitalTwin(ctx context.Context, id string) (*DigitalTwin, error) {
	var raw json.RawMessage
	res, err := c.do(ctx, request{method: http.MethodGet, path: digitalTwinPath(id)}, &raw)
	if err != nil {
		return nil, err
	}
	var env digitalTwinEnvelope
	_ = json.Unmarshal(raw, &env)
	dt := &DigitalTwin{ID: env.ID, ModelID: env.Metadata.Model, ETag: strings.Trim(res.Headers.Get("ETag"), `"`), Document: raw}
	if dt.ID == "" {
		dt.ID = id
	}
	return dt, nil
}

func digitalTwinPath(id string) string { return "/digitaltwins/" + id }

// DigitalTwinCommand is a Plug and Play command invocation: a root-level
// command (ComponentPath empty) or a component command.
type DigitalTwinCommand struct {
	DigitalTwinID string
	ComponentPath string
	CommandName   string
	// Payload is the JSON request payload (any JSON value); nil sends null
	// (verified: accepted).
	Payload json.RawMessage
	// ResponseTimeoutSeconds is how long the service waits for the device's
	// response, ConnectTimeoutSeconds how long it waits for an offline device
	// to connect (same semantics and ranges as direct methods).
	ResponseTimeoutSeconds int64
	ConnectTimeoutSeconds  int64
}

// CommandResult is the device's answer to a digital twin command: the
// device-defined status (x-ms-command-statuscode header) and payload.
type CommandResult struct {
	Status    int64
	Payload   json.RawMessage
	RequestID string
}

// InvokeDigitalTwinCommand calls a Plug and Play command (POST
// /digitaltwins/{id}[/components/{path}]/commands/{name}). Verified: works
// under SAS (policy with ServiceConnect); **rejected with 401
// IotHubUnauthorizedAccess (401002) under Entra ID** regardless of role —
// IsUnauthorized; an offline device answers 404 DeviceNotOnline (errorCode
// 404103, IsDeviceNotOnline); a missing device 404 DeviceNotFound. Like a
// direct method the call is never re-sent after an ambiguous failure.
func (c *Client) InvokeDigitalTwinCommand(ctx context.Context, cmd DigitalTwinCommand) (*CommandResult, error) {
	path := digitalTwinPath(cmd.DigitalTwinID)
	if cmd.ComponentPath != "" {
		path += "/components/" + cmd.ComponentPath
	}
	path += "/commands/" + cmd.CommandName
	q := url.Values{}
	q.Set("connectTimeoutInSeconds", strconv.FormatInt(cmd.ConnectTimeoutSeconds, 10))
	q.Set("responseTimeoutInSeconds", strconv.FormatInt(cmd.ResponseTimeoutSeconds, 10))
	payload := cmd.Payload
	if payload == nil {
		payload = json.RawMessage("null")
	}
	var out json.RawMessage
	r := request{method: http.MethodPost, path: path, query: q, body: payload, retry: perRequest{
		OnlyThrottleRetries: true,
		TryTimeout:          time.Duration(cmd.ResponseTimeoutSeconds+cmd.ConnectTimeoutSeconds)*time.Second + 30*time.Second,
	}}
	res, err := c.do(ctx, r, &out)
	if err != nil {
		return nil, err
	}
	result := &CommandResult{Payload: out, RequestID: res.Headers.Get("x-ms-request-id")}
	if s := res.Headers.Get("x-ms-command-statuscode"); s != "" {
		result.Status, _ = strconv.ParseInt(s, 10, 64)
	}
	return result, nil
}
