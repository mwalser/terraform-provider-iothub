# The outcome of the fw_channel job started by the iothub_scheduled_job example.
data "iothub_scheduled_job" "fw_channel" {
  job_id = "fw-channel-1-4-0"
}

output "fw_channel_rollout" {
  value = {
    status    = data.iothub_scheduled_job.fw_channel.status
    succeeded = try(data.iothub_scheduled_job.fw_channel.device_job_statistics.succeeded_count, 0)
    failed    = try(data.iothub_scheduled_job.fw_channel.device_job_statistics.failed_count, 0)
  }
}
