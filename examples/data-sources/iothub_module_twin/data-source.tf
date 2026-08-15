data "iothub_module_twin" "edge_agent" {
  device_id = "gw-munich-01"
  module_id = "$edgeAgent"
}

output "edge_agent_platform" {
  value = try(jsondecode(data.iothub_module_twin.edge_agent.reported_properties).runtime.platform, null)
}
