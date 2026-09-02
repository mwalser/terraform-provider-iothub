package module

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/mwalser/terraform-provider-iothub/internal/provider/providertest"
)

// A module deleted outside Terraform leaves the state and keeps its identity,
// also for a core that sends none (Terraform < 1.12).
func TestRead_Gone(t *testing.T) {
	providertest.ReadGone(t, NewResource(), map[string]tftypes.Value{
		"id": providertest.Str("dev-1/mod-1"), "device_id": providertest.Str("dev-1"), "module_id": providertest.Str("mod-1"),
	}, map[string]string{"device_id": "dev-1", "module_id": "mod-1"})
}
