package provider_test

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	iotacc "github.com/mwalser/terraform-provider-iothub/internal/acctest"
)

// Actions are exercised through lifecycle action_trigger blocks on a
// terraform_data resource (Terraform ≥ 1.14).
func TestAccActions_purgeApplyAndOfflineMethod(t *testing.T) {
	dev := acctest.RandomWithPrefix("tf-acc")
	base := iotacc.ProviderConfig() + fmt.Sprintf(`
resource "iothub_device" "edge" {
  device_id    = %q
  edge_enabled = true
  authentication = { type = "certificateAuthority" }
}

action "iothub_purge_c2d_queue" "purge" {
  config {
    device_id = iothub_device.edge.device_id
  }
}

action "iothub_set_edge_modules" "apply" {
  config {
    device_id = iothub_device.edge.device_id
    modules_content = jsonencode({
      "$edgeAgent" = {
        "properties.desired" = {
          schemaVersion = "1.1"
          runtime       = { type = "docker", settings = { minDockerVersion = "v1.25" } }
          systemModules = {
            edgeAgent = { type = "docker", settings = { image = "mcr.microsoft.com/azureiotedge-agent:1.4" } }
            edgeHub   = { type = "docker", status = "running", restartPolicy = "always", settings = { image = "mcr.microsoft.com/azureiotedge-hub:1.4" } }
          }
          modules = {}
        }
      }
      "$edgeHub" = {
        "properties.desired" = {
          schemaVersion                = "1.2"
          routes                       = { all = "FROM /messages/* INTO $upstream" }
          storeAndForwardConfiguration = { timeToLiveSecs = 7200 }
        }
      }
    })
  }
}
`, dev)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { iotacc.PreCheck(t) },
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactories,
		CheckDestroy:             iotacc.CheckDeviceDestroyed(dev),
		Steps: []resource.TestStep{
			{
				Config: base + `
resource "terraform_data" "trigger" {
  input = "1"
  lifecycle {
    action_trigger {
      events  = [after_create]
      actions = [action.iothub_purge_c2d_queue.purge, action.iothub_set_edge_modules.apply]
    }
  }
}`,
				Check: func(_ *terraform.State) error {
					// applyConfigurationContent writes the manifest into the $edgeAgent module twin
					tw, err := iotacc.Client(t).GetModuleTwin(context.Background(), dev, "$edgeAgent")
					if err != nil {
						return fmt.Errorf("edgeAgent twin: %w", err)
					}
					var desired map[string]any
					if err := json.Unmarshal(tw.Properties.Desired, &desired); err != nil {
						return err
					}
					if _, ok := desired["systemModules"]; !ok {
						return fmt.Errorf("manifest not applied; $edgeAgent desired = %s", tw.Properties.Desired)
					}
					return nil
				},
			},
			{ // a direct method on an offline device fails clearly
				Config: base + `
action "iothub_direct_method" "reboot" {
  config {
    device_id                = iothub_device.edge.device_id
    method_name              = "reboot"
    payload                  = jsonencode({ delaySec = 1 })
    response_timeout_seconds = 5
  }
}
resource "terraform_data" "trigger" {
  input = "2"
  lifecycle {
    action_trigger {
      events  = [after_update]
      actions = [action.iothub_direct_method.reboot]
    }
  }
}`,
				ExpectError: regexp.MustCompile(`Device not online`),
			},
			{ // a non-edge device rejects applyConfigurationContent
				Config: iotacc.ProviderConfig() + fmt.Sprintf(`
resource "iothub_device" "leaf" {
  device_id = "%s-leaf"
}
action "iothub_set_edge_modules" "apply" {
  config {
    device_id       = iothub_device.leaf.device_id
    modules_content = jsonencode({ "$edgeAgent" = { "properties.desired" = { schemaVersion = "1.1" } } })
  }
}
resource "terraform_data" "trigger_leaf" {
  input = "3"
  lifecycle {
    action_trigger {
      events  = [after_create]
      actions = [action.iothub_set_edge_modules.apply]
    }
  }
}`, dev),
				ExpectError: regexp.MustCompile(`Not an Azure IoT Edge device`),
			},
			{ // clean up the leaf device created by the failed step
				Config: iotacc.ProviderConfig() + fmt.Sprintf(`
resource "iothub_device" "leaf" {
  device_id = "%s-leaf"
}`, dev),
			},
		},
	})
}

func TestAccActions_configValidation(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
action "iothub_direct_method" "bad" {
  config {
    device_id   = "d"
    method_name = "x"
    payload     = "not json"
  }
}
resource "terraform_data" "t" {
  lifecycle {
    action_trigger {
      events  = [after_create]
      actions = [action.iothub_direct_method.bad]
    }
  }
}`,
				ExpectError: regexp.MustCompile(`Invalid payload`),
			},
			{
				Config: `
action "iothub_direct_method" "bad" {
  config {
    device_id                = "d"
    method_name              = "x"
    response_timeout_seconds = 1
  }
}
resource "terraform_data" "t" {
  lifecycle {
    action_trigger {
      events  = [after_create]
      actions = [action.iothub_direct_method.bad]
    }
  }
}`,
				ExpectError: regexp.MustCompile(`must be between 5 and 300`),
			},
			{
				Config: `
action "iothub_set_edge_modules" "bad" {
  config {
    device_id       = "d"
    modules_content = jsonencode({ "$edgeHub" = {} })
  }
}
resource "terraform_data" "t" {
  lifecycle {
    action_trigger {
      events  = [after_create]
      actions = [action.iothub_set_edge_modules.bad]
    }
  }
}`,
				ExpectError: regexp.MustCompile(`must contain .\$edgeAgent.`),
			},
		},
	})
}
