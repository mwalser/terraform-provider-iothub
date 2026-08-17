variable "release" {
  type    = string
  default = "1.4.0"
}

# Push a desired firmware channel to every EU leaf device (device twins).
resource "iothub_configuration" "fw_channel" {
  # Versioned ID + create_before_destroy: no window without a configuration.
  configuration_id = "fw-channel-${replace(var.release, ".", "-")}"
  target_condition = "tags.fleet.region = 'eu' AND tags.kind = 'leaf'"
  priority         = 10
  labels           = { owner = "platform", release = var.release }

  device_content = jsonencode({
    "properties.desired.firmware" = {
      channel = "stable"
      release = var.release
    }
  })

  lifecycle {
    create_before_destroy = true
  }

  metrics = {
    applied = <<-EOT
      SELECT deviceId FROM devices
      WHERE properties.reported.firmware.channel = 'stable'
    EOT
  }
}

# A module-scoped configuration targets module twins.
resource "iothub_configuration" "telemetry_interval" {
  configuration_id = "telemetry-interval"
  target_condition = "FROM devices.modules WHERE moduleId = 'telemetry'"
  priority         = 5
  module_content = jsonencode({
    "properties.desired.interval" = 30
  })
}
