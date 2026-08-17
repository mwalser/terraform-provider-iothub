package provider_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	iotacc "github.com/mwalser/terraform-provider-iothub/internal/acctest"
)

// TestAccEdgeManifest_deployments applies what the function builds: a base
// deployment, a layered deployment and a layered deployment that only adds a
// route (its $edgeAgent is empty), reads them back and checks that a second
// plan is empty — the hub stores the content as built.
func TestAccEdgeManifest_deployments(t *testing.T) {
	id := acctest.RandomWithPrefix("tf-acc")
	cfg := iotacc.ProviderConfig() + fmt.Sprintf(`
variable "acr_password" {
  type      = string
  sensitive = true
  default   = "not-a-real-password"
}

locals {
  base = provider::iothub::edge_manifest({
    registry_credentials = {
      acr = { address = "contoso.azurecr.io", username = "ci", password = var.acr_password }
    }
    edge_agent = { image = "mcr.microsoft.com/azureiotedge-agent:1.5" }
    edge_hub = {
      image = "mcr.microsoft.com/azureiotedge-hub:1.5"
      env   = { OptimizeForPerformance = false }
    }
    modules = {
      tempSensor = {
        image = "mcr.microsoft.com/azureiotedge-simulated-temperature-sensor:1.4"
        # More than 512 characters exercises the edge agent's chunked
        # createOptions representation through an IoT Hub round trip.
        create_options = { Labels = { acceptance = join("", [for _ in range(600) : "x"]) }, HostConfig = { Binds = ["/data:/data"] } }
        env            = { SendInterval = 5, Verbose = true }
        startup_order  = 2
        desired        = { SendData = true, SendInterval = 5 }
      }
    }
    routes = {
      upstream = "FROM /messages/* INTO $upstream"
      alerts   = { route = "FROM /messages/modules/tempSensor/* INTO $upstream", priority = 0, time_to_live_secs = 60 }
    }
    store_and_forward = { time_to_live_secs = 3600, max_size_bytes = 104857600 }
  })
  layer = provider::iothub::edge_manifest({
    layered = true
    modules = {
      filter = {
        image   = "contoso.azurecr.io/filter:2.1.0"
        desired = { threshold = 25 }
      }
    }
    routes = { filtered = "FROM /messages/modules/filter/outputs/* INTO $upstream" }
  })
  routes_only = provider::iothub::edge_manifest({
    layered = true
    routes  = { twins = "FROM /twinChangeNotifications INTO $upstream" }
  })
}

resource "iothub_edge_deployment" "base" {
  deployment_id    = %[1]q
  target_condition = "tags.site = 'munich'"
  priority         = 10
  modules_content  = local.base
}

resource "iothub_edge_deployment" "layer" {
  deployment_id    = "%[1]s-layer"
  target_condition = "tags.site = 'munich'"
  priority         = 20
  modules_content  = local.layer
}

resource "iothub_edge_deployment" "routes_only" {
  deployment_id    = "%[1]s-routes"
  target_condition = "tags.site = 'munich'"
  priority         = 30
  modules_content  = local.routes_only
}

data "iothub_edge_deployment" "base" {
  deployment_id = iothub_edge_deployment.base.deployment_id
}

data "iothub_edge_deployment" "layer" {
  deployment_id = iothub_edge_deployment.layer.deployment_id
}

output "base_matches" {
  value = jsondecode(data.iothub_edge_deployment.base.modules_content) == jsondecode(nonsensitive(local.base))
}

output "layer_matches" {
  value = jsondecode(data.iothub_edge_deployment.layer.modules_content) == jsondecode(local.layer)
}
`, id)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { iotacc.PreCheck(t) },
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactories,
		CheckDestroy:             checkConfigurationDestroyed(id, id+"-layer", id+"-routes"),
		Steps: []resource.TestStep{
			{
				Config: cfg,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectSensitiveValue("iothub_edge_deployment.base", tfjsonpath.New("modules_content")),
				}},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownOutputValue("base_matches", knownvalue.Bool(true)),
					statecheck.ExpectKnownOutputValue("layer_matches", knownvalue.Bool(true)),
					statecheck.ExpectKnownValue("iothub_edge_deployment.routes_only", tfjsonpath.New("modules_content"),
						knownvalue.StringExact(`{"$edgeAgent":{},"$edgeHub":{"properties.desired.routes.twins":"FROM /twinChangeNotifications INTO $upstream"}}`)),
				},
			},
			{
				Config:           cfg,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
		},
	})
}

// TestAccEdgeManifest_setEdgeModules applies a built manifest to one edge
// device through the action.
func TestAccEdgeManifest_setEdgeModules(t *testing.T) {
	dev := acctest.RandomWithPrefix("tf-acc")
	cfg := iotacc.ProviderConfig() + fmt.Sprintf(`
resource "iothub_device" "edge" {
  device_id      = %q
  edge_enabled   = true
  authentication = { type = "certificateAuthority" }
}

action "iothub_set_edge_modules" "apply" {
  config {
    device_id = iothub_device.edge.device_id
    modules_content = provider::iothub::edge_manifest({
      edge_agent = { image = "mcr.microsoft.com/azureiotedge-agent:1.5" }
      edge_hub   = { image = "mcr.microsoft.com/azureiotedge-hub:1.5" }
      routes     = { all = "FROM /messages/* INTO $upstream" }
    })
  }
}

resource "terraform_data" "apply" {
  lifecycle {
    action_trigger {
      events  = [after_create]
      actions = [action.iothub_set_edge_modules.apply]
    }
  }
}
`, dev)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { iotacc.PreCheck(t) },
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactories,
		CheckDestroy:             iotacc.CheckDeviceDestroyed(dev),
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: func(_ *terraform.State) error {
					// applyConfigurationContent writes the manifest into the $edgeAgent module twin.
					tw, err := iotacc.Client(t).GetModuleTwin(context.Background(), dev, "$edgeAgent")
					if err != nil {
						return fmt.Errorf("edgeAgent twin: %w", err)
					}
					var desired map[string]any
					if err := json.Unmarshal(tw.Properties.Desired, &desired); err != nil {
						return err
					}
					if v, _ := desired["schemaVersion"].(string); v != "1.1" {
						return fmt.Errorf("manifest not applied; $edgeAgent desired = %s", tw.Properties.Desired)
					}
					return nil
				},
			},
		},
	})
}
