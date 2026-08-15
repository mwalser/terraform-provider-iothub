# Digital twin commands need SAS authentication (the endpoint rejects Entra ID
# tokens); the equivalent direct method works with both.
provider "iothub" {
  connection_string = var.iothub_service_connection_string # a policy with ServiceConnect
}

resource "iothub_device" "controller" {
  device_id = "controller-0001"
}

# Root-level command of the device's DTDL model.
action "iothub_digital_twin_command" "reboot" {
  config {
    digital_twin_id          = iothub_device.controller.device_id
    command_name             = "reboot"
    payload                  = jsonencode({ delay = 5 })
    response_timeout_seconds = 30
    connect_timeout_seconds  = 60 # wait for a device that is offline right now
  }
}

# Command of a component (thermostat1 in dtmi:com:example:TemperatureController;2).
action "iothub_digital_twin_command" "max_min_report" {
  config {
    digital_twin_id       = iothub_device.controller.device_id
    component_path        = "thermostat1"
    command_name          = "getMaxMinReport"
    payload               = jsonencode("2026-01-01T00:00:00Z")
    expected_status_codes = [200]
  }
}

resource "iothub_device_twin" "controller" {
  device_id = iothub_device.controller.device_id
  desired_properties = jsonencode({
    thermostat1 = { "__t" = "c", targetTemperature = 21.5 }
  })

  lifecycle {
    action_trigger {
      events  = [after_update]
      actions = [action.iothub_digital_twin_command.reboot]
    }
  }
}

# Or ad hoc:  terraform apply -invoke=action.iothub_digital_twin_command.max_min_report
