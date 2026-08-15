data "iothub_statistics" "hub" {}

output "device_counts" {
  value = {
    total     = data.iothub_statistics.hub.total_device_count
    enabled   = data.iothub_statistics.hub.enabled_device_count
    disabled  = data.iothub_statistics.hub.disabled_device_count
    connected = data.iothub_statistics.hub.connected_device_count
  }
}
