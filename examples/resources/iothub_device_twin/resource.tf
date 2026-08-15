resource "iothub_device" "sensor" {
  device_id = "sensor-0001"
}

# Terraform manages exactly these keys: tags.site, tags.fleet.region,
# tags.fleet.ring, desired.telemetryIntervalSec and desired.firmware.channel.
# Anything else in the twin is neither read nor written. That includes other
# top-level keys and other systems' keys inside `fleet` or `firmware`.
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

# Several resources can share one twin as long as they declare different keys.
resource "iothub_device_twin" "sensor_ops" {
  device_id = iothub_device.sensor.device_id
  tags      = jsonencode({ ops = { oncall = "team-a" } })
}
