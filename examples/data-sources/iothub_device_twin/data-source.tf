data "iothub_device_twin" "sensor" {
  device_id = "sensor-0001"
}

# Reported properties are written by the device. The iothub_device_twin
# resource does not expose them. Read them here.
output "sensor_firmware_version" {
  value = try(jsondecode(data.iothub_device_twin.sensor.reported_properties).firmware.version, null)
}
