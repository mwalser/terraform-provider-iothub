data "iothub_digital_twin" "controller" {
  device_id = "controller-0001"
}

output "controller_model" {
  value = data.iothub_digital_twin.controller.model_id
}

locals {
  digital_twin = jsondecode(data.iothub_digital_twin.controller.document)
  # Components are the non-$ keys that carry their own $metadata.
  components = [for k, v in local.digital_twin : k if !startswith(k, "$") && can(v["$metadata"])]
}

output "controller_components" {
  value = local.components
}

output "thermostat_max_temperature" {
  value = try(local.digital_twin.thermostat1.maxTempSinceLastReboot, null)
}

# Writable properties are twin desired properties. "__t" = "c" marks a component.
resource "iothub_device_twin" "controller" {
  device_id = "controller-0001"
  desired_properties = jsonencode({
    thermostat1 = { "__t" = "c", targetTemperature = 21.5 }
  })
}
