resource "iothub_device" "sensor" {
  device_id = "sensor-0001"
}

# A 24-hour device token, signed with the primary key.
ephemeral "iothub_sas_token" "sensor" {
  device_id = iothub_device.sensor.device_id
  ttl       = "24h"
}

# The secret is rewritten (with a fresh token) on the first apply of each hour.
resource "azurerm_key_vault_secret" "sensor_token" {
  name             = "sensor-0001-sas"
  key_vault_id     = azurerm_key_vault.devices.id
  value_wo         = ephemeral.iothub_sas_token.sensor.token
  value_wo_version = tonumber(formatdate("YYYYMMDDhh", plantimestamp()))
}

resource "iothub_module" "telemetry" {
  device_id = iothub_device.sensor.device_id
  module_id = "telemetry"
}

# A module token, signed with the secondary key.
ephemeral "iothub_sas_token" "telemetry" {
  device_id = iothub_module.telemetry.device_id
  module_id = iothub_module.telemetry.module_id
  key       = "secondary"
}
