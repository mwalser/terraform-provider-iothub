terraform {
  required_version = ">= 1.14"
  required_providers {
    iothub = {
      source  = "mwalser/iothub"
      version = "~> 1.0"
    }
  }
}

provider "iothub" {
  hostname = "contoso-prod.azure-devices.net"
}

resource "iothub_device" "gateway" {
  device_id    = "gw-munich-01"
  edge_enabled = true
}

resource "iothub_device" "sensor" {
  device_id    = "sensor-0001"
  parent_scope = iothub_device.gateway.device_scope
}

resource "iothub_device_twin" "sensor" {
  device_id = iothub_device.sensor.device_id
  tags      = jsonencode({ site = "munich" })
  desired_properties = jsonencode({
    telemetryIntervalSec = 60
  })
}
