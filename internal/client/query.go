package client

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
)

// Query item types reported in x-ms-item-type.
const (
	ItemTypeRaw       = "Raw"       // projections (SELECT deviceId, …)
	ItemTypeTwin      = "Twin"      // SELECT * FROM devices / devices.modules
	ItemTypeDeviceJob = "DeviceJob" // FROM devices.jobs
)

// queryPageSize is the page size requested per call.
const queryPageSize = 100

// QueryPage is one page of query results.
type QueryPage struct {
	Items        []json.RawMessage
	Continuation string // empty on the last page
	ItemType     string
}

// QueryPage runs one page of an IoT Hub query language statement (POST
// /devices/query). continuation is the token from the previous page or "".
func (c *Client) QueryPage(ctx context.Context, query string, continuation string) (*QueryPage, error) {
	h := http.Header{"X-Ms-Max-Item-Count": []string{strconv.Itoa(queryPageSize)}}
	if continuation != "" {
		h.Set("X-Ms-Continuation", continuation)
	}
	var items []json.RawMessage
	res, err := c.do(ctx, request{method: http.MethodPost, path: "/devices/query", body: map[string]string{"query": query}, headers: h}, &items)
	if err != nil {
		return nil, err
	}
	return &QueryPage{Items: items, Continuation: res.Headers.Get("X-Ms-Continuation"), ItemType: res.Headers.Get("X-Ms-Item-Type")}, nil
}

// Query runs a query to completion, following continuation tokens. The
// item type is that of the first page.
func (c *Client) Query(ctx context.Context, query string) ([]json.RawMessage, string, error) {
	var all []json.RawMessage
	itemType, continuation := "", ""
	for {
		page, err := c.QueryPage(ctx, query, continuation)
		if err != nil {
			return nil, "", err
		}
		all = append(all, page.Items...)
		if itemType == "" {
			itemType = page.ItemType
		}
		if page.Continuation == "" {
			return all, itemType, nil
		}
		continuation = page.Continuation
	}
}
