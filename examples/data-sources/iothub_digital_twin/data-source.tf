data "iothub_digital_twin" "controller" {
  digital_twin_id = "controller-0001"
}

# The DTDL model the device announced when it connected (null for non-PnP devices).
output "controller_model" {
  value = data.iothub_digital_twin.controller.model_id
}

# The document is the hub's Plug and Play view: root properties and components
# (objects with their own $metadata), derived from the device twin.
locals {
  digital_twin = jsondecode(data.iothub_digital_twin.controller.document)
  components   = [for k, v in local.digital_twin : k if !startswith(k, "$") && can(v["$metadata"])]
}

output "controller_components" {
  value = local.components
}

output "thermostat_max_temperature" {
  value = try(local.digital_twin.thermostat1.maxTempSinceLastReboot, null)
}

# Writable PnP properties are twin desired properties — manage them with
# iothub_device_twin (component properties carry the "__t" = "c" marker).
resource "iothub_device_twin" "controller" {
  device_id = "controller-0001"
  desired_properties = jsonencode({
    thermostat1 = { "__t" = "c", targetTemperature = 21.5 }
  })
}
