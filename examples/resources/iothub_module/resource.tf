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

# Keys that never enter state: both keys as write-only arguments, plus a
# version to rotate. Change the version whenever you change a key.
ephemeral "random_bytes" "diagnostics_primary" {
  length = 32
}

ephemeral "random_bytes" "diagnostics_secondary" {
  length = 32
}

resource "iothub_module" "diagnostics" {
  device_id                = iothub_device.sensor.device_id
  module_id                = "diagnostics"
  primary_key_wo           = ephemeral.random_bytes.diagnostics_primary.base64
  primary_key_wo_version   = 1
  secondary_key_wo         = ephemeral.random_bytes.diagnostics_secondary.base64
  secondary_key_wo_version = 1
}
