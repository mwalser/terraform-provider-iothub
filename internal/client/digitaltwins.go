package client

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
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
