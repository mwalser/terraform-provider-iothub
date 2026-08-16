# A plain device with hub-generated SAS keys (stored sensitive in state).
resource "iothub_device" "sensor" {
  device_id = "sensor-0001"
}

# An IoT Edge gateway authenticating with a CA-signed certificate.
resource "iothub_device" "gateway" {
  device_id    = "gw-munich-01"
  edge_enabled = true
  authentication = {
    type = "certificateAuthority"
  }
}

# A downstream (leaf) device behind the gateway, using a self-signed certificate.
resource "iothub_device" "downstream" {
  device_id    = "sensor-0002"
  parent_scope = iothub_device.gateway.device_scope
  authentication = {
    type               = "selfSigned"
    primary_thumbprint = "aabbccddeeff00112233445566778899aabbccdd"
  }
}

# Keys that never enter state. Bump the version to rotate.
variable "key_rotation" {
  type    = number
  default = 1
}

ephemeral "random_bytes" "meter_primary" {
  length = 32
}

ephemeral "random_bytes" "meter_secondary" {
  length = 32
}

resource "iothub_device" "meter" {
  device_id                = "meter-0001"
  status                   = "disabled"
  status_reason            = "awaiting commissioning"
  primary_key_wo           = ephemeral.random_bytes.meter_primary.base64
  primary_key_wo_version   = var.key_rotation
  secondary_key_wo         = ephemeral.random_bytes.meter_secondary.base64
  secondary_key_wo_version = var.key_rotation
}
