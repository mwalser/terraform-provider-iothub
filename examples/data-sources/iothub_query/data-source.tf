# Device IDs and rings of every device in the Munich site (a projection: item_type "Raw").
data "iothub_query" "munich" {
  query = "SELECT deviceId, tags.fleet.ring AS ring FROM devices WHERE tags.site = 'munich'"
}

locals {
  munich_devices = [for row in data.iothub_query.munich.results : jsondecode(row).deviceId]
}

# Whole twins (item_type "Twin").
data "iothub_query" "stale_firmware" {
  query = "SELECT * FROM devices WHERE properties.reported.firmware.version != properties.desired.firmware.version"
}

output "stale_firmware_count" {
  value = data.iothub_query.stale_firmware.result_count
}
