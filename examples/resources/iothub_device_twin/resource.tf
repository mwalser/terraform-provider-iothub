resource "iothub_device" "sensor" {
  device_id = "sensor-0001"
}

# Manages exactly these keys: tags.site, tags.fleet.region, tags.fleet.ring,
# desired.telemetryIntervalSec and desired.firmware.channel. The rest of the
# twin is left alone.
resource "iothub_device_twin" "sensor" {
  device_id = iothub_device.sensor.device_id

  tags = jsonencode({
    site  = "munich"
    fleet = { region = "eu", ring = 2 }
  })

  desired_properties = jsonencode({
    telemetryIntervalSec = 60
    firmware             = { channel = "stable" }
  })
}
