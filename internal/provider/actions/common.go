// Package actions implements the provider's actions — the "verbs" of the
// Service API that keep no state (CONCEPT.md §9): direct methods, applying
// a deployment manifest to an edge device, purging the cloud-to-device
// queue, scheduled jobs, import/export jobs and job cancellation.
package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/common"
)

// hubClient returns the hub client for an action invocation.
func hubClient(pd *common.ProviderData) (*client.Client, diag.Diagnostics) { return pd.HubOrError() }

// progress sends a progress message to Terraform (shown during apply) if the
// framework provided a sender.
func progress(resp *action.InvokeResponse, format string, args ...any) {
	if resp.SendProgress != nil {
		resp.SendProgress(action.InvokeProgressEvent{Message: fmt.Sprintf(format, args...)})
	}
}

// compactJSON renders a JSON value for messages, truncated.
func compactJSON(raw json.RawMessage, limit int) string {
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return "null"
	}
	var v any
	if json.Unmarshal(raw, &v) == nil {
		if b, err := json.Marshal(v); err == nil {
			s = string(b)
		}
	}
	if len(s) > limit {
		return s[:limit] + "…"
	}
	return s
}

// configure is the shared ActionWithConfigure implementation.
type configured struct {
	pd *common.ProviderData
}

func (c *configured) Configure(_ context.Context, req action.ConfigureRequest, resp *action.ConfigureResponse) {
	pd, diags := common.ExpectProviderData(req.ProviderData)
	resp.Diagnostics.Append(diags...)
	c.pd = pd
}

// objectAsOptions tolerates null/unknown nested values when decoding.
var objectAsOptions = basetypes.ObjectAsOptions{UnhandledNullAsEmpty: true, UnhandledUnknownAsEmpty: true}
