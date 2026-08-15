resource "iothub_device" "sensor" {
  device_id = "sensor-0001"
}

# Read at apply time, never stored in state or plan.
ephemeral "iothub_device_credentials" "sensor" {
  device_id = iothub_device.sensor.device_id
}

# Hand the connection string to a write-only argument.
resource "azurerm_key_vault_secret" "sensor" {
  name             = "sensor-0001-connection-string"
  key_vault_id     = azurerm_key_vault.devices.id
  value_wo         = ephemeral.iothub_device_credentials.sensor.primary_connection_string
  value_wo_version = 1
}
