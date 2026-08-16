resource "iothub_device" "gateway" {
  device_id      = "gw-lab-01"
  edge_enabled   = true
  authentication = { type = "certificateAuthority" }
}

# Push a manifest to a single lab gateway right away (like `az iot edge set-modules`).
action "iothub_set_edge_modules" "lab" {
  config {
    device_id       = iothub_device.gateway.device_id
    modules_content = jsonencode(jsondecode(file("${path.module}/deployment.json")).modulesContent)
  }
}

resource "terraform_data" "lab_release" {
  input = filesha256("${path.module}/deployment.json")

  lifecycle {
    action_trigger {
      events  = [after_create, after_update]
      actions = [action.iothub_set_edge_modules.lab]
    }
  }
}
