# Cancel a scheduled job that has not run yet, for example the reboot job
# scheduled by the iothub_scheduled_job example, or a running import/export job.
action "iothub_cancel_job" "reboot_gateways" {
  config {
    job_id = "reboot-gateways-${formatdate("YYYY-MM-DD", plantimestamp())}"
    kind   = "scheduled"
  }
}

action "iothub_cancel_job" "export" {
  config {
    job_id = "2799ff3b-8a5d-44cb-ad3e-b13fff329736"
    kind   = "import_export"
  }
}

# Ad hoc:  terraform apply -invoke=action.iothub_cancel_job.reboot_gateways
