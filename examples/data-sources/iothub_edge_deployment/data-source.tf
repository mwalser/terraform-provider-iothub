data "iothub_edge_deployment" "base" {
  deployment_id = "base-1-4-0"
}

locals {
  metrics = data.iothub_edge_deployment.base.system_metrics
}

output "base_deployment_health" {
  value = {
    targeted = try(local.metrics["targetedCount"], 0)
    applied  = try(local.metrics["appliedCount"], 0)
    failed   = try(local.metrics["reportedFailedCount"], 0)
  }
}
