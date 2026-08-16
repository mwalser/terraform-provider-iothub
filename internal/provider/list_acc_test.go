package provider_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/querycheck"
	"github.com/hashicorp/terraform-plugin-testing/querycheck/queryfilter"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	iotacc "github.com/mwalser/terraform-provider-iothub/internal/acctest"
)

// waitForQuery polls the query index until the query returns want rows.
func waitForQuery(t *testing.T, query string, want int) {
	t.Helper()
	c := iotacc.Client(t)
	deadline := time.Now().Add(90 * time.Second)
	for {
		items, _, err := c.Query(context.Background(), query)
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		if len(items) == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("query index did not catch up: %d rows for %s, want %d", len(items), query, want)
		}
		time.Sleep(3 * time.Second)
	}
}

func TestAccDeviceList_query(t *testing.T) {
	marker := acctest.RandomWithPrefix("tfacc")
	dev1, dev2 := marker+"-a", marker+"-b"
	create := iotacc.ProviderConfig() + fmt.Sprintf(`
resource "iothub_device" "a" {
  device_id = %q
}
resource "iothub_device" "b" {
  device_id     = %q
  edge_enabled  = true
  authentication = { type = "certificateAuthority" }
}
resource "iothub_device_twin" "a" {
  device_id = iothub_device.a.device_id
  tags      = jsonencode({ tfacc = %q })
}
resource "iothub_device_twin" "b" {
  device_id = iothub_device.b.device_id
  tags      = jsonencode({ tfacc = %q })
}`, dev1, dev2, marker, marker)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { iotacc.PreCheck(t) },
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactories,
		CheckDestroy:             iotacc.CheckDeviceDestroyed(dev1, dev2),
		Steps: []resource.TestStep{
			{Config: create},
			{
				PreConfig: func() {
					waitForQuery(t, fmt.Sprintf("SELECT deviceId FROM devices WHERE tags.tfacc = '%s'", marker), 2)
				},
				Query: true,
				Config: fmt.Sprintf(`
list "iothub_device" "tagged" {
  provider = iothub
  config {
    query_condition = "tags.tfacc = '%s'"
  }
}
list "iothub_device" "edge" {
  provider         = iothub
  include_resource = true
  config {
    query_condition = "tags.tfacc = '%s' AND capabilities.iotEdge = true"
  }
}`, marker, marker),
				QueryResultChecks: []querycheck.QueryResultCheck{
					querycheck.ExpectLength("iothub_device.tagged", 2),
					querycheck.ExpectIdentity("iothub_device.tagged", map[string]knownvalue.Check{"device_id": knownvalue.StringExact(dev1)}),
					querycheck.ExpectIdentity("iothub_device.tagged", map[string]knownvalue.Check{"device_id": knownvalue.StringExact(dev2)}),
					querycheck.ExpectLength("iothub_device.edge", 1),
					querycheck.ExpectResourceDisplayName("iothub_device.edge",
						queryfilter.ByResourceIdentity(map[string]knownvalue.Check{"device_id": knownvalue.StringExact(dev2)}),
						knownvalue.StringExact(dev2)),
					querycheck.ExpectResourceKnownValues("iothub_device.edge",
						queryfilter.ByResourceIdentity(map[string]knownvalue.Check{"device_id": knownvalue.StringExact(dev2)}),
						[]querycheck.KnownValueCheck{
							{Path: tfjsonpath.New("edge_enabled"), KnownValue: knownvalue.Bool(true)},
							{Path: tfjsonpath.New("authentication").AtMapKey("type"), KnownValue: knownvalue.StringExact("certificateAuthority")},
							{Path: tfjsonpath.New("id"), KnownValue: knownvalue.StringExact(dev2)},
						}),
				},
			},
			{ // import by identity works too
				Config: create + fmt.Sprintf(`
import {
  to = iothub_device.imported
  identity = { device_id = %q }
}
resource "iothub_device" "imported" {
  device_id = %q
}`, dev1, dev1),
				ExpectNonEmptyPlan: false,
			},
		},
	})
}

func TestAccModuleAndConfigurationList_query(t *testing.T) {
	marker := acctest.RandomWithPrefix("tfacc")
	dev := marker + "-dev"
	create := iotacc.ProviderConfig() + fmt.Sprintf(`
resource "iothub_device" "d" {
  device_id    = %q
  edge_enabled = true
  authentication = { type = "certificateAuthority" }
}
resource "iothub_module" "m1" {
  device_id  = iothub_device.d.device_id
  module_id  = "telemetry"
  managed_by = "tfacc"
}
resource "iothub_module" "m2" {
  device_id = iothub_device.d.device_id
  module_id = "updater"
}
resource "iothub_configuration" "c" {
  configuration_id = "%s-cfg"
  target_condition = "tags.tfacc = '%s'"
  device_content   = jsonencode({ "properties.desired.x" = 1 })
}
resource "iothub_edge_deployment" "e" {
  deployment_id    = "%s-edge"
  target_condition = "tags.tfacc = '%s'"
  modules_content  = jsonencode({ "$edgeAgent" = { "properties.desired.modules.x" = { type = "docker", settings = { image = "x" } } } })
}`, dev, marker, marker, marker, marker)
	hubID := func(id string) map[string]knownvalue.Check {
		return map[string]knownvalue.Check{"configuration_id": knownvalue.StringExact(id)}
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { iotacc.PreCheck(t) },
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactories,
		CheckDestroy:             resource.ComposeTestCheckFunc(iotacc.CheckDeviceDestroyed(dev), checkConfigurationDestroyed(marker+"-cfg", marker+"-edge")),
		Steps: []resource.TestStep{
			{Config: create},
			{
				PreConfig: func() {
					// two custom modules plus $edgeAgent/$edgeHub on the edge device
					waitForQuery(t, fmt.Sprintf("SELECT deviceId, moduleId FROM devices.modules WHERE deviceId = '%s'", dev), 4)
				},
				Query: true,
				Config: fmt.Sprintf(`
list "iothub_module" "of_device" {
  provider         = iothub
  include_resource = true
  config {
    device_id = %q
  }
}
list "iothub_module" "managed" {
  provider = iothub
  config {
    device_id       = %q
    query_condition = "moduleId = 'telemetry'"
  }
}
list "iothub_configuration" "all" {
  provider = iothub
  config {}
}
list "iothub_edge_deployment" "all" {
  provider         = iothub
  include_resource = true
  config {}
}`, dev, dev),
				QueryResultChecks: []querycheck.QueryResultCheck{
					// system modules are skipped
					querycheck.ExpectLength("iothub_module.of_device", 2),
					querycheck.ExpectIdentity("iothub_module.of_device", map[string]knownvalue.Check{
						"device_id": knownvalue.StringExact(dev), "module_id": knownvalue.StringExact("telemetry"),
					}),
					querycheck.ExpectResourceKnownValues("iothub_module.of_device",
						queryfilter.ByResourceIdentity(map[string]knownvalue.Check{"device_id": knownvalue.StringExact(dev), "module_id": knownvalue.StringExact("telemetry")}),
						[]querycheck.KnownValueCheck{{Path: tfjsonpath.New("managed_by"), KnownValue: knownvalue.StringExact("tfacc")}}),
					querycheck.ExpectLength("iothub_module.managed", 1),
					// configurations and deployments are separated by content kind
					querycheck.ExpectIdentity("iothub_configuration.all", hubID(marker+"-cfg")),
					querycheck.ExpectNoIdentity("iothub_configuration.all", hubID(marker+"-edge")),
					querycheck.ExpectIdentity("iothub_edge_deployment.all", map[string]knownvalue.Check{"deployment_id": knownvalue.StringExact(marker + "-edge")}),
					querycheck.ExpectNoIdentity("iothub_edge_deployment.all", map[string]knownvalue.Check{"deployment_id": knownvalue.StringExact(marker + "-cfg")}),
					querycheck.ExpectResourceKnownValues("iothub_edge_deployment.all",
						queryfilter.ByResourceIdentity(map[string]knownvalue.Check{"deployment_id": knownvalue.StringExact(marker + "-edge")}),
						[]querycheck.KnownValueCheck{
							{Path: tfjsonpath.New("target_condition"), KnownValue: knownvalue.StringExact("tags.tfacc = '" + marker + "'")},
							{Path: tfjsonpath.New("priority"), KnownValue: knownvalue.Int64Exact(0)},
						}),
				},
			},
			{ // identity import for a module and a configuration
				Config: create + fmt.Sprintf(`
import {
  to = iothub_module.imported
  identity = { device_id = %q, module_id = "updater" }
}
resource "iothub_module" "imported" {
  device_id = %q
  module_id = "updater"
}
import {
  to = iothub_configuration.imported
  identity = { configuration_id = "%s-cfg" }
}
resource "iothub_configuration" "imported" {
  configuration_id = "%s-cfg"
  target_condition = "tags.tfacc = '%s'"
  device_content   = jsonencode({ "properties.desired.x" = 1 })
}`, dev, dev, marker, marker, marker),
			},
		},
	})
}
