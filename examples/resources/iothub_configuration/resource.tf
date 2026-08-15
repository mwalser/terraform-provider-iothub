# Push a desired firmware channel to every EU leaf device (device twins).
resource "iothub_configuration" "fw_channel" {
  configuration_id = "fw-channel-stable"
  target_condition = "tags.fleet.region = 'eu' AND tags.kind = 'leaf'"
  priority         = 10
  labels           = { owner = "platform" }

  # Immutable: changing the content replaces the configuration.
  device_content = jsonencode({
    "properties.desired.firmware" = { channel = "stable" }
  })

  # Custom metrics; results appear in `metric_results`.
  metrics = {
    applied = "SELECT deviceId FROM devices WHERE properties.reported.firmware.channel = 'stable'"
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
