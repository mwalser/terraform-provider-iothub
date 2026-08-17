variable "acr_password" {
  type      = string
  sensitive = true
}

variable "hub_resource_id" {
  type = string
}

resource "iothub_edge_deployment" "base" {
  deployment_id    = "base-1-4-0"
  target_condition = "tags.site = 'munich'"
  priority         = 10

  modules_content = provider::iothub::edge_manifest({
    registry_credentials = {
      acr = {
        address  = "contoso.azurecr.io"
        username = "contoso"
        password = var.acr_password
      }
    }
    edge_agent = { image = "mcr.microsoft.com/azureiotedge-agent:1.5" }
    edge_hub   = { image = "mcr.microsoft.com/azureiotedge-hub:1.5" }
    modules = {
      tempSensor = {
        image   = "mcr.microsoft.com/azureiotedge-simulated-temperature-sensor:1.4"
        env     = { SendInterval = 5 }
        desired = { SendData = true }
      }
      filter = {
        image          = "contoso.azurecr.io/filter:2.1.0"
        create_options = { HostConfig = { Binds = ["/data:/data"] } }
        startup_order  = 2
      }
    }
    routes = {
      sensorToFilter = "FROM /messages/modules/tempSensor/* INTO BrokeredEndpoint(\"/modules/filter/inputs/input1\")"
      upstream = {
        route    = "FROM /messages/modules/filter/* INTO $upstream"
        priority = 0
      }
    }
    store_and_forward = { time_to_live_secs = 3600 }
  })

  lifecycle {
    create_before_destroy = true
  }
}

# A layered deployment adds a module and a route on top of the base.
resource "iothub_edge_deployment" "metrics" {
  deployment_id    = "metrics-1-3-0"
  target_condition = "tags.site = 'munich' AND tags.metrics = 'on'"
  priority         = 20

  modules_content = provider::iothub::edge_manifest({
    layered = true
    modules = {
      metricsCollector = {
        image = "mcr.microsoft.com/azureiotedge-metrics-collector:1.3"
        env = {
          UploadTarget = "IotMessage"
          ResourceId   = var.hub_resource_id
        }
      }
    }
    routes = {
      metrics = "FROM /messages/modules/metricsCollector/* INTO $upstream"
    }
  })

  lifecycle {
    create_before_destroy = true
  }
}
