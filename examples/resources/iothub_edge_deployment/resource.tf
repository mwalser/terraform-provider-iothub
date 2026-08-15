variable "release" {
  type    = string
  default = "1.4.0"
}

# A base deployment from a standard deployment manifest file.
resource "iothub_edge_deployment" "base" {
  # A changed manifest replaces the deployment. A version in the ID plus
  # create_before_destroy avoids a window without a deployment.
  deployment_id    = "base-${replace(var.release, ".", "-")}"
  target_condition = "tags.site = 'munich'"
  priority         = 10
  labels           = { release = var.release }

  modules_content = jsonencode(jsondecode(file("${path.module}/deployment.json")).modulesContent)

  metrics = {
    healthy = "SELECT deviceId FROM devices.modules WHERE moduleId = '$edgeHub' AND properties.reported.lastDesiredStatus.code = 200"
  }

  lifecycle {
    create_before_destroy = true
  }
}

# A layered deployment adds a module on top of the base deployment.
resource "iothub_edge_deployment" "temp_sensor" {
  deployment_id    = "temp-sensor-${replace(var.release, ".", "-")}"
  target_condition = "tags.site = 'munich' AND tags.sensors = 'temp'"
  priority         = 20
  modules_content = jsonencode({
    "$edgeAgent" = {
      "properties.desired.modules.tempSensor" = {
        type          = "docker"
        status        = "running"
        restartPolicy = "always"
        settings      = { image = "mcr.microsoft.com/azureiotedge-simulated-temperature-sensor:1.4" }
      }
    }
  })
}
