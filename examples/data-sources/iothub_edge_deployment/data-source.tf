data "iothub_edge_deployment" "base" {
  deployment_id = "base-1-4-0"
}

output "base_deployment_health" {
  value = {
    targeted = try(data.iothub_edge_deployment.base.system_metrics["targetedCount"], 0)
    applied  = try(data.iothub_edge_deployment.base.system_metrics["appliedCount"], 0)
    failed   = try(data.iothub_edge_deployment.base.system_metrics["reportedFailedCount"], 0)
  }
}
