package common

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
	"github.com/mwalser/terraform-provider-iothub/internal/twinpatch"
)

// ProviderData is handed to every construct via the framework's
// ResourceData/DataSourceData/EphemeralResourceData/ActionData.
type ProviderData struct {
	Settings Settings
	// Client talks to the configured hub. It is nil while the hub's hostname
	// is unknown, which happens during the first plan of a configuration that
	// also creates the hub (`hostname = azurerm_iothub.x.hostname`); the
	// provider is configured again with the real value at apply time.
	Client *client.Client
	// Refresh gates the twin-first refresh of identities (see RefreshGate).
	Refresh RefreshGate
}

// Hub returns the client for the configured hub. It returns ok=false without
// diagnostics while the hostname is not knowable yet (see Client), so the
// caller can defer or return unknown values.
func (pd *ProviderData) Hub() (c *client.Client, ok bool, diags diag.Diagnostics) {
	if pd == nil {
		diags.AddError("Provider not configured", "The provider was not configured before this operation; this is a bug in the provider.")
		return nil, false, diags
	}
	if pd.Client == nil {
		return nil, false, nil
	}
	return pd.Client, true, nil
}

// HubOrError is Hub for operations that only run at apply time (create,
// update, delete, import, actions), where the hostname is always known: an
// unconfigured hub is reported as an error.
func (pd *ProviderData) HubOrError() (*client.Client, diag.Diagnostics) {
	c, ok, diags := pd.Hub()
	if diags.HasError() {
		return nil, diags
	}
	if !ok {
		diags.AddError("IoT Hub hostname unknown", "The provider's `hostname` (or `connection_string`) is not known yet. Apply the resources it depends on first.")
		return nil, diags
	}
	return c, diags
}

// DeferIfHubUnknown marks a resource plan as deferred when the hub is not
// known yet and Terraform allows deferral; otherwise the plan proceeds
// without contacting the hub.
func DeferIfHubUnknown(pd *ProviderData, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if pd == nil {
		return
	}
	if _, ok, diags := pd.Hub(); !ok && !diags.HasError() && req.ClientCapabilities.DeferralAllowed {
		resp.Deferred = &resource.Deferred{Reason: resource.DeferredReasonProviderConfigUnknown}
	}
}

// DataSourceHub returns the hub client for a data source read. A data source
// is read at plan time, so while the hostname is unknown the read is deferred
// when Terraform allows that and reported as an error otherwise; ok is false
// in both cases and the caller returns.
func DataSourceHub(pd *ProviderData, req datasource.ReadRequest, resp *datasource.ReadResponse) (c *client.Client, ok bool) {
	c, ok, diags := pd.Hub()
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return nil, false
	}
	if ok {
		return c, true
	}
	if req.ClientCapabilities.DeferralAllowed {
		resp.Deferred = &datasource.Deferred{Reason: datasource.DeferredReasonProviderConfigUnknown}
		return nil, false
	}
	resp.Diagnostics.AddError("IoT Hub hostname unknown during plan",
		"This data source is read while planning, but the provider's `hostname` (or `connection_string`) is not known "+
			"until apply, typically because the hub is created in the same configuration. Apply the hub first "+
			"(for example with `-target`), or move the data source to a configuration that runs after the hub exists.")
	return nil, false
}

// EphemeralHub returns the hub client for opening an ephemeral resource.
// Ephemeral resources are opened at plan time too, so while the hostname is
// unknown the open is deferred when Terraform allows that. Otherwise a
// warning is added and ok is false without an error: the caller then reports
// its values as unknown, and Terraform opens the resource again at apply
// time when the hub is known.
func EphemeralHub(pd *ProviderData, req ephemeral.OpenRequest, resp *ephemeral.OpenResponse) (c *client.Client, ok bool) {
	c, ok, diags := pd.Hub()
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return nil, false
	}
	if ok {
		return c, true
	}
	if req.ClientCapabilities.DeferralAllowed {
		resp.Deferred = &ephemeral.Deferred{Reason: ephemeral.DeferredReasonProviderConfigUnknown}
		return nil, false
	}
	resp.Diagnostics.AddWarning("IoT Hub not known yet",
		"The provider's `hostname` (or `connection_string`) is not known until apply, so these values are known after apply.")
	return nil, false
}

// ExpectProviderData asserts the framework handed us our ProviderData; nil is
// legitimate while the provider is not yet configured (validation phase).
func ExpectProviderData(raw any) (*ProviderData, diag.Diagnostics) {
	var diags diag.Diagnostics
	if raw == nil {
		return nil, nil
	}
	pd, ok := raw.(*ProviderData)
	if !ok {
		diags.AddError("Unexpected provider data", fmt.Sprintf("Expected *common.ProviderData, got %T. This is a bug in the provider.", raw))
		return nil, diags
	}
	return pd, nil
}

// RawJSONString renders a raw JSON section from the hub as a compact string
// value, null when the section is absent or JSON null. Objects are
// re-encoded canonically so equal documents compare equal.
func RawJSONString(raw []byte) types.String {
	if len(raw) == 0 || string(raw) == "null" {
		return types.StringNull()
	}
	if doc, err := twinpatch.Decode(string(raw)); err == nil {
		return types.StringValue(twinpatch.Encode(doc))
	}
	return types.StringValue(string(raw))
}

// QueryIDs runs a projection query and returns the values of column (a
// string) from every row, in order. Rows without the column are skipped.
func QueryIDs(ctx context.Context, c *client.Client, query, column string) ([]string, diag.Diagnostics) {
	var diags diag.Diagnostics
	items, _, err := c.Query(ctx, query)
	if err != nil {
		diags.AddError("IoT Hub query failed", "Query: "+query+"\n\n"+DescribeError(err))
		return nil, diags
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		var row map[string]any
		if err := json.Unmarshal(it, &row); err != nil {
			continue
		}
		if v, ok := row[column].(string); ok && v != "" {
			out = append(out, v)
		}
	}
	return out, diags
}

// NullTimeouts is the timeouts block value of resources that were listed
// rather than configured.
func NullTimeouts() timeouts.Value {
	return timeouts.Value{Object: types.ObjectNull(map[string]attr.Type{
		"create": types.StringType, "read": types.StringType, "update": types.StringType, "delete": types.StringType,
	})}
}

// TimeoutsOpts is the timeouts block every resource declares: all four
// operations, each with the same default and a description that names it.
func TimeoutsOpts(def string) timeouts.Opts {
	desc := func(op string) string {
		return "How long to wait for the " + op + " to finish, including retries of throttled requests (default `" + def + "`), for example `30m`."
	}
	return timeouts.Opts{
		Create: true, Read: true, Update: true, Delete: true,
		CreateDescription: desc("create"), ReadDescription: desc("read"), UpdateDescription: desc("update"), DeleteDescription: desc("delete"),
	}
}
