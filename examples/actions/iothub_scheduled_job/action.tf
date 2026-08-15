# Roll a desired-property change out to a fleet with a scheduled twin update
# job (runs immediately, waits for completion, fails if any device failed).
action "iothub_scheduled_job" "fw_channel" {
  config {
    job_id                     = "fw-channel-${var.release}"
    type                       = "scheduleUpdateTwin"
    query_condition            = "tags.fleet.region = 'eu'"
    max_execution_time_seconds = 3600
    twin_patch = {
      desired_properties = jsonencode({ firmware = { channel = "stable", version = var.release } })
    }
  }
}

# Reboot every gateway of a site tonight. Do not wait for the run.
action "iothub_scheduled_job" "reboot_gateways" {
  config {
    type            = "scheduleDeviceMethod"
    query_condition = "tags.site = 'munich' AND capabilities.iotEdge = true"
    start_time      = "2026-08-16T02:00:00Z" # at most 168 h ahead
    method = {
      name                     = "reboot"
      payload                  = jsonencode({ delaySec = 30 })
      response_timeout_seconds = 60
    }
    wait = false
  }
}

resource "terraform_data" "release" {
  input = var.release
  lifecycle {
    action_trigger {
      events  = [after_create, after_update]
      actions = [action.iothub_scheduled_job.fw_channel]
    }
  }
}

variable "release" {
  type    = string
  default = "1.4.0"
}
