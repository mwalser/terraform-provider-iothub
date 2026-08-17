data "iothub_module_twin" "agent" {
  device_id = "gw-munich-01"
  module_id = "$edgeAgent"
}

locals {
  reported = jsondecode(data.iothub_module_twin.agent.reported_properties)
}

output "edge_agent_platform" {
  value = try(local.reported.runtime.platform, null)
}
