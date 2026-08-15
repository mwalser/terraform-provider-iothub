package common

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
)

// ProviderData is handed to every construct via the framework's
// ResourceData/DataSourceData/EphemeralResourceData/ActionData.
type ProviderData struct {
	Settings Settings
	// HostnameUnknown is true when the provider-level hostname was unknown
	// at configure time (it typically references azurerm_iothub.x.hostname
	// during the first plan). Constructs without their own hostname must
	// then defer.
	HostnameUnknown bool
	// Clients creates per-hub clients sharing one pipeline and credential.
	Clients *client.Factory
	// Refresh gates the twin-first refresh of identities (see RefreshGate).
	Refresh RefreshGate
}

// HostnameAttributeDescription documents the per-construct hostname attribute.
const HostnameAttributeDescription = "Hostname of the IoT Hub, in lowercase (`<hub>.azure-devices.net`). " +
	"Defaults to the provider's `hostname`. Set it here to manage several hubs from one provider block, or to " +
	"reference a hub that does not exist yet (`azurerm_iothub.x.hostname`)."

// ResolveHostname picks the hub for a construct: its own `hostname` attribute
// if set, otherwise the provider default. It returns ok=false without
// diagnostics when the hostname is not knowable yet (unknown value during
// plan) so the caller can defer or mark results unknown; it returns an error
// diagnostic when no hostname is configured anywhere.
func ResolveHostname(own types.String, pd *ProviderData) (hostname string, ok bool, diags diag.Diagnostics) {
	if own.IsUnknown() {
		return "", false, nil
	}
	if !own.IsNull() && strings.TrimSpace(own.ValueString()) != "" {
		return strings.TrimSpace(own.ValueString()), true, nil
	}
	if pd.HostnameUnknown {
		return "", false, nil
	}
	if pd.Settings.Hostname == "" {
		diags.AddAttributeError(path.Root("hostname"), "No IoT Hub hostname configured",
			"Set `hostname` on this block or on the provider block (or the IOTHUB_HOSTNAME environment variable).")
		return "", false, diags
	}
	return pd.Settings.Hostname, true, nil
}

// ClientFor returns the client for the resolved hostname or a diagnostic
// explaining why none can be built (e.g. SAS policy bound to another hub).
func (pd *ProviderData) ClientFor(_ context.Context, hostname string) (*client.Client, diag.Diagnostics) {
	var diags diag.Diagnostics
	if pd == nil || pd.Clients == nil {
		diags.AddError("Provider not configured", "The provider was not configured before this operation; this is a bug in the provider.")
		return nil, diags
	}
	c, err := pd.Clients.Client(hostname)
	if err != nil {
		diags.AddAttributeError(path.Root("hostname"), "Cannot address IoT Hub", err.Error())
		return nil, diags
	}
	return c, nil
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
