package provider_test

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	iotacc "github.com/mwalser/terraform-provider-iothub/internal/acctest"
)

// The function is pure: these tests run Terraform without a hub.

func TestEdgeManifest_output(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
output "manifest" {
  value = provider::iothub::edge_manifest({
    edge_agent = { image = "mcr.microsoft.com/azureiotedge-agent:1.5" }
    edge_hub   = { image = "mcr.microsoft.com/azureiotedge-hub:1.5" }
    modules = {
      tempSensor = {
        image          = "mcr.microsoft.com/azureiotedge-simulated-temperature-sensor:1.4"
        create_options = { HostConfig = { Binds = ["/data:/data"] } }
        env            = { SendInterval = 5, Verbose = true, Mode = "fast" }
        startup_order  = 2
        desired        = { SendData = true, SendInterval = 5 }
      }
    }
    routes = {
      upstream = "FROM /messages/* INTO $upstream"
      alerts   = { route = "FROM /messages/modules/tempSensor/* INTO $upstream", priority = 0, time_to_live_secs = 60 }
    }
    store_and_forward = { time_to_live_secs = 3600 }
  })
}
output "layered" {
  value = provider::iothub::edge_manifest({
    layered = true
    modules = { filter = { image = "contoso.azurecr.io/filter:2.1.0", status = "stopped" } }
  })
}`,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownOutputValue("manifest", knownvalue.StringExact(
						`{"$edgeAgent":{"properties.desired":{"modules":{"tempSensor":{"env":{"Mode":{"value":"fast"},"SendInterval":{"value":5},"Verbose":{"value":true}},"restartPolicy":"always","settings":{"createOptions":"{\"HostConfig\":{\"Binds\":[\"/data:/data\"]}}","image":"mcr.microsoft.com/azureiotedge-simulated-temperature-sensor:1.4"},"startupOrder":2,"status":"running","type":"docker"}},"runtime":{"settings":{"registryCredentials":{}},"type":"docker"},"schemaVersion":"1.1","systemModules":{"edgeAgent":{"settings":{"createOptions":"{}","image":"mcr.microsoft.com/azureiotedge-agent:1.5"},"type":"docker"},"edgeHub":{"restartPolicy":"always","settings":{"createOptions":"{\"HostConfig\":{\"PortBindings\":{\"443/tcp\":[{\"HostPort\":\"443\"}],\"5671/tcp\":[{\"HostPort\":\"5671\"}],\"8883/tcp\":[{\"HostPort\":\"8883\"}]}}}","image":"mcr.microsoft.com/azureiotedge-hub:1.5"},"status":"running","type":"docker"}}}},"$edgeHub":{"properties.desired":{"routes":{"alerts":{"priority":0,"route":"FROM /messages/modules/tempSensor/* INTO $upstream","timeToLiveSecs":60},"upstream":"FROM /messages/* INTO $upstream"},"schemaVersion":"1.1","storeAndForwardConfiguration":{"timeToLiveSecs":3600}}},"tempSensor":{"properties.desired":{"SendData":true,"SendInterval":5}}}`,
					)),
					statecheck.ExpectKnownOutputValue("layered", knownvalue.StringExact(
						`{"$edgeAgent":{"properties.desired.modules.filter":{"restartPolicy":"always","settings":{"createOptions":"{}","image":"contoso.azurecr.io/filter:2.1.0"},"status":"stopped","type":"docker"}}}`,
					)),
				},
			},
		},
	})
}

func TestEdgeManifest_sensitiveAndUnknown(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
variable "acr_password" {
  type      = string
  sensitive = true
  default   = "s3cret"
}
# A sensitive value anywhere in the manifest makes the result sensitive.
resource "terraform_data" "with_secret" {
  input = provider::iothub::edge_manifest({
    registry_credentials = { acr = { address = "contoso.azurecr.io", username = "ci", password = var.acr_password } }
    edge_agent           = { image = "mcr.microsoft.com/azureiotedge-agent:1.5" }
    edge_hub             = { image = "mcr.microsoft.com/azureiotedge-hub:1.5" }
  })
}
# A value known only after apply makes the result unknown until then.
resource "terraform_data" "tag" {
  input = "1.5"
}
resource "terraform_data" "with_unknown" {
  input = provider::iothub::edge_manifest({
    edge_agent = { image = "mcr.microsoft.com/azureiotedge-agent:${terraform_data.tag.output}" }
    edge_hub   = { image = "mcr.microsoft.com/azureiotedge-hub:${terraform_data.tag.output}" }
  })
}`,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectSensitiveValue("terraform_data.with_secret", tfjsonpath.New("input")),
						plancheck.ExpectUnknownValue("terraform_data.with_unknown", tfjsonpath.New("input")),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("terraform_data.with_unknown", tfjsonpath.New("output"), knownvalue.StringRegexp(regexp.MustCompile(`"image":"mcr\.microsoft\.com/azureiotedge-agent:1\.5"`))),
					statecheck.ExpectKnownValue("terraform_data.with_secret", tfjsonpath.New("output"), knownvalue.StringRegexp(regexp.MustCompile(`"password":"s3cret"`))),
				},
			},
		},
	})
}

func TestEdgeManifest_errors(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
output "bad" {
  value = provider::iothub::edge_manifest({
    edge_agent = { image = "mcr.microsoft.com/azureiotedge-agent:1.5" }
    edge_hub   = { image = "mcr.microsoft.com/azureiotedge-hub:1.5" }
    modules    = { m = { image = "i", restartPolicy = "always" } }
  })
}`,
				ExpectError: regexp.MustCompile(`(?s)modules\["m"\]\.restartPolicy: unknown\s+key; accepted: create_options,.*restart_policy, startup_order,\s+status, version`),
			},
			{
				Config: `
output "bad" {
  value = provider::iothub::edge_manifest({
    layered = true
    edge_hub = { image = "mcr.microsoft.com/azureiotedge-hub:1.5" }
    routes   = { r = "SELECT * FROM devices" }
  })
}`,
				ExpectError: regexp.MustCompile(`2 problems:`),
			},
		},
	})
}
