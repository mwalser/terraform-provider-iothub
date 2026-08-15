data "iothub_module" "edge_agent" {
  device_id = "gw-munich-01"
  module_id = "$edgeAgent"
}

output "edge_agent_connection_state" {
  value = data.iothub_module.edge_agent.connection_state
}
