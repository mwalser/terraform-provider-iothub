data "iothub_device_twin" "sensor" {
  device_id = "sensor-0001"
}

output "sensor_firmware_version" {
  value = try(jsondecode(data.iothub_device_twin.sensor.reported_properties).firmware.version, null)
}
