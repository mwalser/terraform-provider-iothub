variable "release" {
  type    = string
  default = "1.4.0"
}

# Roll a desired-property change out to a fleet with a scheduled twin update
# job. It runs immediately, waits for completion and fails if any device failed.
# The job ID must be new for every run while the hub still remembers the
# earlier job, so it carries the release. Job IDs allow only lowercase letters,
# digits and hyphens.
action "iothub_scheduled_job" "fw_channel" {
  config {
    job_id                     = "fw-channel-${replace(var.release, ".", "-")}"
    query_condition            = "tags.fleet.region = 'eu'"
    max_execution_time_seconds = 3600
    twin_patch = {
      desired_properties = jsonencode({ firmware = { channel = "stable", version = var.release } })
    }
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

# Reboot every gateway of a site eight hours from now. Do not wait for the run.
# The same ID is used by the iothub_cancel_job example to cancel it.
action "iothub_scheduled_job" "reboot_gateways" {
  config {
    job_id          = "reboot-gateways-${formatdate("YYYY-MM-DD", plantimestamp())}"
    query_condition = "tags.site = 'munich' AND capabilities.iotEdge = true"
    start_time      = timeadd(plantimestamp(), "8h") # at most 7 days ahead
    method = {
      name                     = "reboot"
      payload                  = jsonencode({ delaySec = 30 })
      response_timeout_seconds = 60
    }
    wait = false
  }
}

# Ad hoc:  terraform apply -invoke=action.iothub_scheduled_job.reboot_gateways
