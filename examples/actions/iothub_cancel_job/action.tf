# A scheduled job that has not run yet (the reboot job of the
# iothub_scheduled_job example):
#   terraform apply -invoke=action.iothub_cancel_job.reboot_gateways
action "iothub_cancel_job" "reboot_gateways" {
  config {
    job_id = "reboot-${formatdate("YYYY-MM-DD", plantimestamp())}"
    kind   = "scheduled"
  }
}

# A running import or export job:
#   terraform apply -invoke=action.iothub_cancel_job.export
action "iothub_cancel_job" "export" {
  config {
    job_id = "2799ff3b-8a5d-44cb-ad3e-b13fff329736"
    kind   = "import_export"
  }
}
