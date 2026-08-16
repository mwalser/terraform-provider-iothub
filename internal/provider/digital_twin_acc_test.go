package provider_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	iotacc "github.com/mwalser/terraform-provider-iothub/internal/acctest"
)

// The digital twin endpoint answers for every device (verified): a device
// that never announced a model has an empty $model. A positive command
// invocation needs a connected PnP device and is verified manually against a
// simulator (see CONCEPT.md Appendix D); here the offline path and the
// authentication requirement are exercised.
func TestAccDigitalTwin_dataSourceAndCommand(t *testing.T) {
	dev := acctest.RandomWithPrefix("tf-acc")
	base := iotacc.ProviderConfig() + fmt.Sprintf(`
resource "iothub_device" "d" {
  device_id = %q
}
`, dev)
	// Under Entra ID the service rejects the command endpoint outright
	// (401); under SAS the offline device is what fails.
	commandError := regexp.MustCompile(`Digital twin commands need SAS authentication`)
	if iotacc.UsingSAS() {
		commandError = regexp.MustCompile(`Device not online`)
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { iotacc.PreCheck(t) },
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactories,
		CheckDestroy:             iotacc.CheckDeviceDestroyed(dev),
		Steps: []resource.TestStep{
			{
				Config: base + `
data "iothub_digital_twin" "d" {
  digital_twin_id = iothub_device.d.device_id
}`,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("data.iothub_digital_twin.d", tfjsonpath.New("id"), knownvalue.StringExact(dev)),
					statecheck.ExpectKnownValue("data.iothub_digital_twin.d", tfjsonpath.New("model_id"), knownvalue.Null()),
					statecheck.ExpectKnownValue("data.iothub_digital_twin.d", tfjsonpath.New("document"), knownvalue.StringRegexp(regexp.MustCompile(`"\$dtId":\s*"`+regexp.QuoteMeta(dev)+`"`))),
					statecheck.ExpectKnownValue("data.iothub_digital_twin.d", tfjsonpath.New("document"), knownvalue.StringRegexp(regexp.MustCompile(`"\$model":\s*""`))),
					statecheck.ExpectKnownValue("data.iothub_digital_twin.d", tfjsonpath.New("etag"), knownvalue.NotNull()),
				},
			},
			{ // root-level command on an offline device
				Config: base + `
action "iothub_digital_twin_command" "reboot" {
  config {
    digital_twin_id          = iothub_device.d.device_id
    command_name             = "reboot"
    payload                  = jsonencode({ delay = 1 })
    response_timeout_seconds = 5
  }
}
resource "terraform_data" "trigger" {
  input = "1"
  lifecycle {
    action_trigger {
      events  = [after_create]
      actions = [action.iothub_digital_twin_command.reboot]
    }
  }
}`,
				ExpectError: commandError,
			},
			{ // component command, same outcome
				Config: base + `
action "iothub_digital_twin_command" "report" {
  config {
    digital_twin_id          = iothub_device.d.device_id
    component_path           = "thermostat1"
    command_name             = "getMaxMinReport"
    response_timeout_seconds = 5
  }
}
resource "terraform_data" "trigger2" {
  input = "1"
  lifecycle {
    action_trigger {
      events  = [after_create]
      actions = [action.iothub_digital_twin_command.report]
    }
  }
}`,
				ExpectError: commandError,
			},
			{ // a missing device is reported as such
				Config: base + `
data "iothub_digital_twin" "missing" {
  digital_twin_id = "does-not-exist-tf-acc"
}`,
				ExpectError: regexp.MustCompile(`Digital twin not found`),
			},
		},
	})
}

func TestAccDigitalTwinCommand_configValidation(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: iotacc.ProviderConfig() + `
action "iothub_digital_twin_command" "bad" {
  config {
    digital_twin_id = "d1"
    component_path  = "not-a-dtdl-name"
    command_name    = "reboot"
  }
}
resource "terraform_data" "t" {
  lifecycle {
    action_trigger {
      events  = [after_create]
      actions = [action.iothub_digital_twin_command.bad]
    }
  }
}`,
				ExpectError: regexp.MustCompile(`must be a DTDL name`),
			},
			{
				Config: iotacc.ProviderConfig() + `
action "iothub_digital_twin_command" "bad" {
  config {
    digital_twin_id = "d1"
    command_name    = "reboot"
    payload         = "{not json"
  }
}
resource "terraform_data" "t" {
  lifecycle {
    action_trigger {
      events  = [after_create]
      actions = [action.iothub_digital_twin_command.bad]
    }
  }
}`,
				ExpectError: regexp.MustCompile(`Invalid payload`),
			},
		},
	})
}
