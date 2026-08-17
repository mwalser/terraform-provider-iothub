# The outcome of the fw_channel job of the iothub_scheduled_job example.
data "iothub_scheduled_job" "fw_channel" {
  job_id = "firmware-1-4-0"
}

output "fw_channel_status" {
  value = data.iothub_scheduled_job.fw_channel.status
}

output "fw_channel_statistics" {
  value = data.iothub_scheduled_job.fw_channel.device_job_statistics
}
