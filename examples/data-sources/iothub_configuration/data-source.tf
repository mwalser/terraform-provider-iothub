data "iothub_configuration" "fw_channel" {
  configuration_id = "fw-channel-stable"
}

output "fw_channel_metrics" {
  value = data.iothub_configuration.fw_channel.system_metrics
}
