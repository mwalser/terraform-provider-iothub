# Custom modules of one device (the hub's $edgeAgent / $edgeHub are skipped).
list "iothub_module" "gateway" {
  provider = iothub

  config {
    device_id = "gw-munich-01"
  }
}

# Every "telemetry" module across the fleet.
list "iothub_module" "telemetry" {
  provider = iothub

  config {
    query_condition = "moduleId = 'telemetry'"
  }
}
