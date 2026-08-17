data "iothub_import_export_job" "export" {
  job_id = "2799ff3b-8a5d-44cb-ad3e-b13fff329736"
}

output "export_status" {
  value = {
    status   = data.iothub_import_export_job.export.status
    progress = data.iothub_import_export_job.export.progress
  }
}
