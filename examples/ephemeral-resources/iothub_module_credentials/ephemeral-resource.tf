resource "iothub_module" "telemetry" {
  device_id = "sensor-0001"
  module_id = "telemetry"
}

# Read at apply time, never stored in state or plan.
ephemeral "iothub_module_credentials" "telemetry" {
  device_id = iothub_module.telemetry.device_id
  module_id = iothub_module.telemetry.module_id
}

# Hand the connection string to a write-only argument.
resource "azurerm_key_vault_secret" "telemetry" {
  name             = "sensor-0001-telemetry-connection-string"
  key_vault_id     = azurerm_key_vault.devices.id
  value_wo         = ephemeral.iothub_module_credentials.telemetry.primary_connection_string
  value_wo_version = 1
}
