resource "iothub_device" "sensor" {
  device_id = "sensor-0001"
}

# Reboot the device whenever its desired properties change.
action "iothub_direct_method" "reboot" {
  config {
    device_id                = iothub_device.sensor.device_id
    method_name              = "reboot"
    payload                  = jsonencode({ delaySec = 5 })
    response_timeout_seconds = 90
    connect_timeout_seconds  = 60 # wait for a device that is offline right now, within the 90 s
    expected_status_codes    = [200, 202]
  }
}

resource "iothub_device_twin" "sensor" {
  device_id          = iothub_device.sensor.device_id
  desired_properties = jsonencode({ telemetryIntervalSec = 60 })

  lifecycle {
    action_trigger {
      events  = [after_update]
      actions = [action.iothub_direct_method.reboot]
    }
  }
}
