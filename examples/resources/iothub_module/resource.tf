resource "iothub_device" "sensor" {
  device_id = "sensor-0001"
}

# A module with hub-generated SAS keys (stored sensitive in state).
resource "iothub_module" "telemetry" {
  device_id  = iothub_device.sensor.device_id
  module_id  = "telemetry"
  managed_by = "platform-team"
}

# A module authenticating with a CA-signed certificate.
resource "iothub_module" "updater" {
  device_id = iothub_device.sensor.device_id
  module_id = "updater"
  authentication = {
    type = "certificateAuthority"
  }
}

# Keys that never enter state. Bump the version to rotate.
ephemeral "random_bytes" "logger_primary" {
  length = 32
}

ephemeral "random_bytes" "logger_secondary" {
  length = 32
}

resource "iothub_module" "logger" {
  device_id                = iothub_device.sensor.device_id
  module_id                = "logger"
  primary_key_wo           = ephemeral.random_bytes.logger_primary.base64
  primary_key_wo_version   = 1
  secondary_key_wo         = ephemeral.random_bytes.logger_secondary.base64
  secondary_key_wo_version = 1
}
