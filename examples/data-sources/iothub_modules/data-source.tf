data "iothub_modules" "gateway" {
  device_id = "gw-munich-01"
}

# Module IDs that are not managed by IoT Edge itself.
output "custom_modules" {
  value = [for m in data.iothub_modules.gateway.modules : m.module_id if m.managed_by != "iotEdge"]
}
