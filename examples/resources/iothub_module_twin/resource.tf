resource "iothub_module" "telemetry" {
  device_id = "sensor-0001"
  module_id = "telemetry"
}

# Manages exactly desired.interval and desired.sinks. The rest is left alone.
resource "iothub_module_twin" "telemetry" {
  device_id = iothub_module.telemetry.device_id
  module_id = iothub_module.telemetry.module_id

  desired_properties = jsonencode({
    interval = 30
    sinks    = ["hub", "local"]
  })
}
