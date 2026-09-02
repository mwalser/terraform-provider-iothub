package configuration

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/mwalser/terraform-provider-iothub/internal/provider/providertest"
)

// A configuration or deployment deleted outside Terraform leaves the state
// and keeps its identity, also for a core that sends none (Terraform < 1.12).
func TestRead_Gone(t *testing.T) {
	providertest.ReadGone(t, NewEdgeDeploymentResource(), map[string]tftypes.Value{
		"id": providertest.Str("dep-1"), "deployment_id": providertest.Str("dep-1"),
		"target_condition": providertest.Str("*"), "modules_content": providertest.Str(`{"$edgeAgent":{}}`),
	}, map[string]string{"deployment_id": "dep-1"})
	providertest.ReadGone(t, NewConfigurationResource(), map[string]tftypes.Value{
		"id": providertest.Str("cfg-1"), "configuration_id": providertest.Str("cfg-1"),
		"target_condition": providertest.Str("*"), "device_content": providertest.Str(`{"properties.desired.x":1}`),
	}, map[string]string{"configuration_id": "cfg-1"})
}
