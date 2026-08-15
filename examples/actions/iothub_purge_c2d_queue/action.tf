resource "iothub_device" "sensor" {
  device_id = "sensor-0001"
}

# Drop stale cloud-to-device messages before a device is re-commissioned.
action "iothub_purge_c2d_queue" "sensor" {
  config {
    device_id = iothub_device.sensor.device_id
  }
}

# Ad hoc:  terraform apply -invoke=action.iothub_purge_c2d_queue.sensor
