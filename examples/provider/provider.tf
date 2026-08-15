terraform {
  required_version = ">= 1.14"
  required_providers {
    iothub = {
      source = "mwalser/iothub"
    }
  }
}

# Entra ID (default). Credentials come from the usual ARM_* / AZURE_*
# environment variables, a workload identity, a managed identity or the
# Azure CLI login. The identity needs an IoT Hub data-plane role on the hub
# (e.g. "IoT Hub Data Contributor") — Owner/Contributor are not enough.
provider "iothub" {
  hostname = "contoso-prod.azure-devices.net"
}

# Shared access policy (SAS) instead of Entra ID:
#
# provider "iothub" {
#   connection_string = azurerm_iothub_shared_access_policy.terraform.primary_connection_string
# }
