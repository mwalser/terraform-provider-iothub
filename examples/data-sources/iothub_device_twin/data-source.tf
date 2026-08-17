data "iothub_device_twin" "sensor" {
  device_id = "sensor-0001"
}

locals {
  reported = jsondecode(data.iothub_device_twin.sensor.reported_properties)
}

output "sensor_firmware_version" {
  value = try(local.reported.firmware.version, null)
}
