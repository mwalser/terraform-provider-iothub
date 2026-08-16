# A scheduled job that has not run yet (the iothub_scheduled_job example's reboot job).
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
