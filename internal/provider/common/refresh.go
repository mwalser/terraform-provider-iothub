package common

import (
	"context"
	"sync"

	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
)

// RefreshGate implements the ETag-gated refresh of device and module
// identities (CONCEPT.md §11.2): a refresh reads the twin first (twin reads
// are two orders of magnitude cheaper than registry reads on S1 — 100/s
// against 1.67/s — and share no throttle with them). The twin's `deviceEtag`
// is the identity's ETag (verified for devices and modules, and verified to
// move on every identity write while staying put across connects and
// telemetry), so an equal ETag proves the identity — keys included — is
// unchanged and only the volatile fields need refreshing, which the twin
// carries. Otherwise the registry is read as before. There is no knob: the
// behaviour is lossless, and a credential that cannot read twins (Entra ID
// without `twins/read`, a SAS policy without ServiceConnect) is detected on
// the first refresh per hub and the registry read is used silently for the
// rest of the run.
type RefreshGate struct {
	twinsUnreadable sync.Map // hostname → struct{}
}

// TwinIfUnchanged reads the twin behind an identity and returns it when the
// identity is provably unchanged since the last refresh: the twin's
// deviceEtag equals etag (the ETag in state) and the connection state is
// still connectionState (the twin has no connectionStateUpdatedTime, so a
// changed connection state must be read from the registry). Any other
// outcome — a changed ETag, an unreadable twin, no ETag in state — returns
// nil and the caller reads the registry as usual. A 401/403 marks the hub so
// the twin is not tried again in this run.
func (g *RefreshGate) TwinIfUnchanged(ctx context.Context, hostname string, read func(context.Context) (*client.Twin, error), etag, connectionState string) *client.Twin {
	if g == nil || etag == "" {
		return nil
	}
	if _, skip := g.twinsUnreadable.Load(hostname); skip {
		return nil
	}
	tw, err := read(ctx)
	if err != nil {
		if client.IsUnauthorized(err) {
			g.twinsUnreadable.Store(hostname, struct{}{})
			tflog.Info(ctx, "credential cannot read twins on this hub; identities are refreshed from the registry for the rest of the run",
				map[string]any{"hostname": hostname, "error": err.Error()})
		} else {
			tflog.Debug(ctx, "twin read failed; refreshing the identity from the registry", map[string]any{"hostname": hostname, "error": err.Error()})
		}
		return nil
	}
	switch {
	case tw.DeviceETag != etag:
		tflog.Debug(ctx, "identity changed since the last refresh; reading the registry", map[string]any{"hostname": hostname, "state_etag": etag, "device_etag": tw.DeviceETag})
		return nil
	case tw.ConnectionState != connectionState:
		tflog.Debug(ctx, "connection state changed; reading the registry for its timestamp", map[string]any{"hostname": hostname})
		return nil
	}
	tflog.Debug(ctx, "identity unchanged; volatile fields refreshed from the twin", map[string]any{"hostname": hostname, "etag": etag})
	return tw
}
