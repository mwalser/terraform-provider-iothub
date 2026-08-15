resource "iothub_device" "sensor" {
  device_id = "sensor-0001"
}

# A 24-hour device token, minted from the primary key at apply time and
# never written to state or plan.
ephemeral "iothub_device_sas_token" "sensor" {
  device_id = iothub_device.sensor.device_id
  ttl       = "24h"
}

# Hand it to a write-only argument, e.g. a Key Vault secret the device
# provisioning pipeline reads.
resource "azurerm_key_vault_secret" "sensor_token" {
  name             = "sensor-0001-sas"
  key_vault_id     = azurerm_key_vault.devices.id
  value_wo         = ephemeral.iothub_device_sas_token.sensor.token
  value_wo_version = 1
}

# Module tokens sign for <hostname>/devices/<device_id>/modules/<module_id>.
ephemeral "iothub_device_sas_token" "telemetry" {
  device_id = iothub_device.sensor.device_id
  module_id = "telemetry"
  key       = "secondary"
}
