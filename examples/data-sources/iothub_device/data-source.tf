data "iothub_device" "gateway" {
  device_id = "gw-munich-01"
}

output "gateway_scope" {
  value = data.iothub_device.gateway.device_scope
}
