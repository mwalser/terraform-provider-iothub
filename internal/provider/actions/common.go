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
	"github.com/hashicorp/terraform-plugin-framework/action/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/common"
)

// hostnameAttribute is the per-action hub override.
func hostnameAttribute() schema.StringAttribute {
	return schema.StringAttribute{MarkdownDescription: common.HostnameAttributeDescription, Optional: true}
}

// clientFor resolves the hub for an action invocation. Unknown values cannot
// occur at invoke time, so an unresolvable hostname is an error.
func clientFor(ctx context.Context, pd *common.ProviderData, hostname types.String) (*client.Client, diag.Diagnostics) {
	host, ok, diags := common.ResolveHostname(hostname, pd)
	if diags.HasError() {
		return nil, diags
	}
	if !ok {
		diags.AddAttributeError(path.Root("hostname"), "IoT Hub hostname unknown", "Set `hostname` in the action's config or on the provider block.")
		return nil, diags
	}
	c, d := pd.ClientFor(ctx, host)
	diags.Append(d...)
	return c, diags
}

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
