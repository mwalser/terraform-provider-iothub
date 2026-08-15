# Discover the devices of a site with terraform query, for example to generate import blocks:
#   terraform query -generate-config-out=devices.tf
list "iothub_device" "munich" {
  provider = iothub

  config {
    query_condition = "tags.site = 'munich'"
  }
}

# All IoT Edge gateways of the hub.
list "iothub_device" "gateways" {
  provider = iothub

  config {
    query_condition = "capabilities.iotEdge = true"
  }
}
