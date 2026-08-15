resource "iothub_device" "sensor" {
  device_id = "sensor-0001"
}

# Terraform owns exactly these leaf paths: tags.site, tags.fleet.region,
# tags.fleet.ring, desired.telemetryIntervalSec and desired.firmware.channel.
# Anything else in the twin — other keys, and other systems' keys inside
# `fleet` or `firmware` — is neither read nor written.
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

# Several resources can share one twin as long as they own different leaves.
resource "iothub_device_twin" "sensor_ops" {
  device_id = iothub_device.sensor.device_id
  tags      = jsonencode({ ops = { oncall = "team-a" } })
}
