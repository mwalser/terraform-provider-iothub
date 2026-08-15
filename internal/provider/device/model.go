// Package device implements iothub_device (resource, data source) and the
// iothub_device_credentials ephemeral resource. Behaviour follows CONCEPT.md
// §6.1, §11.1 and §11.3 and the service facts in Appendix D; the shared
// authentication handling lives in package identity.
package device

import (
	"fmt"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/identity"
)

// parentScopePrefix is how edge device scopes look (ms-azure-iot-edge://<id>-<gen>).
const parentScopePrefix = "ms-azure-iot-edge://"

// parentScopeOf derives the single parent relationship the resource exposes:
// edge devices carry it in parentScopes, leaf devices in deviceScope.
func parentScopeOf(d *client.Device) string {
	if d.Capabilities != nil && d.Capabilities.IotEdge {
		if len(d.ParentScopes) > 0 {
			return d.ParentScopes[0]
		}
		return ""
	}
	return d.DeviceScope
}

// writtenFields are the identity fields the provider writes, i.e. the ones
// conflict inspection compares (CONCEPT.md §11.1). Volatile fields
// (connection state, activity time, message count) are deliberately absent.
type writtenFields struct {
	Status, StatusReason string
	IotEdge              bool
	ParentScope          string
	Auth                 identity.WrittenAuth
}

func writtenFromHub(d *client.Device) writtenFields {
	w := writtenFields{
		Status:      d.Status,
		IotEdge:     d.Capabilities != nil && d.Capabilities.IotEdge,
		ParentScope: parentScopeOf(d),
		Auth:        identity.WrittenAuthFromHub(d.Authentication),
	}
	if d.StatusReason != nil {
		w.StatusReason = *d.StatusReason
	}
	return w
}

// diffWritten lists the written fields that differ between what the plan
// was built from (prior) and what the hub holds now (fresh).
func diffWritten(prior, fresh writtenFields) []string {
	var out []string
	out = identity.DiffString(out, "status", prior.Status, fresh.Status)
	out = identity.DiffString(out, "status_reason", prior.StatusReason, fresh.StatusReason)
	if prior.IotEdge != fresh.IotEdge {
		out = append(out, fmt.Sprintf("edge_enabled: %v → %v", prior.IotEdge, fresh.IotEdge))
	}
	out = identity.DiffString(out, "parent_scope", prior.ParentScope, fresh.ParentScope)
	return append(out, identity.DiffAuth(prior.Auth, fresh.Auth)...)
}
