package common

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
)

func TestRefreshGate_TwinIfUnchanged(t *testing.T) {
	ctx := context.Background()
	twin := &client.Twin{DeviceID: "d1", DeviceETag: "ETAG1", ConnectionState: "Disconnected", LastActivityTime: "2026-08-15T00:00:00Z"}
	reads := 0
	ok := func(context.Context) (*client.Twin, error) { reads++; return twin, nil }

	var g RefreshGate
	// unchanged identity: the twin is returned
	if tw := g.TwinIfUnchanged(ctx, "hub", ok, "ETAG1", "Disconnected"); tw != twin {
		t.Errorf("expected the twin for an unchanged identity, got %v", tw)
	}
	// changed identity ETag → registry read
	if tw := g.TwinIfUnchanged(ctx, "hub", ok, "ETAG0", "Disconnected"); tw != nil {
		t.Error("expected nil for a changed deviceEtag")
	}
	// changed connection state → registry read (the twin lacks the timestamp)
	if tw := g.TwinIfUnchanged(ctx, "hub", ok, "ETAG1", "Connected"); tw != nil {
		t.Error("expected nil for a changed connection state")
	}
	// no ETag in state (import) → registry read without touching the twin
	before := reads
	if tw := g.TwinIfUnchanged(ctx, "hub", ok, "", ""); tw != nil || reads != before {
		t.Error("expected no twin read without a state ETag")
	}
	// a nil gate is inert
	var nilGate *RefreshGate
	if tw := nilGate.TwinIfUnchanged(ctx, "hub", ok, "ETAG1", "Disconnected"); tw != nil {
		t.Error("nil gate must not return a twin")
	}
}

func TestRefreshGate_RemembersUnauthorizedHubs(t *testing.T) {
	ctx := context.Background()
	calls := map[string]int{}
	forbidden := func(status int) func(context.Context) (*client.Twin, error) {
		return func(context.Context) (*client.Twin, error) {
			calls["read"]++
			return nil, &client.Error{StatusCode: status, Code: "IotHubUnauthorizedAccess"}
		}
	}
	var g RefreshGate
	for _, status := range []int{http.StatusForbidden, http.StatusUnauthorized} {
		g = RefreshGate{}
		calls["read"] = 0
		if tw := g.TwinIfUnchanged(ctx, "hub-a", forbidden(status), "E", ""); tw != nil {
			t.Fatal("expected nil on unauthorized")
		}
		// the hub is remembered: no further twin reads for hub-a …
		if tw := g.TwinIfUnchanged(ctx, "hub-a", forbidden(status), "E", ""); tw != nil || calls["read"] != 1 {
			t.Errorf("status %d: expected the twin read to be skipped after 401/403, reads = %d", status, calls["read"])
		}
		// … but other hubs are still tried
		g.TwinIfUnchanged(ctx, "hub-b", forbidden(status), "E", "")
		if calls["read"] != 2 {
			t.Errorf("status %d: expected hub-b to be tried, reads = %d", status, calls["read"])
		}
	}
	// other errors (404, transport) fall back without remembering
	g = RefreshGate{}
	n := 0
	notFound := func(context.Context) (*client.Twin, error) {
		n++
		if n == 1 {
			return nil, &client.Error{StatusCode: http.StatusNotFound, Code: "DeviceNotFound"}
		}
		return nil, errors.New("dial tcp: i/o timeout")
	}
	g.TwinIfUnchanged(ctx, "hub-c", notFound, "E", "")
	g.TwinIfUnchanged(ctx, "hub-c", notFound, "E", "")
	if n != 2 {
		t.Errorf("expected both reads to be attempted, got %d", n)
	}
}
