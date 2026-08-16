terraform {
  required_version = ">= 1.14"
  required_providers {
    iothub = {
      source  = "mwalser/iothub"
      version = "~> 1.0"
    }
  }
}

# Entra ID by default, configured like azurerm: ARM_* variables, use_oidc,
# use_msi or the Azure CLI login. See Authentication below.
provider "iothub" {
  hostname = "contoso-prod.azure-devices.net"
}

# A shared access policy instead:
#
# provider "iothub" {
#   connection_string = azurerm_iothub_shared_access_policy.terraform.primary_connection_string
# }

# A second hub, selected on resources with `provider = iothub.staging`:
#
# provider "iothub" {
#   alias    = "staging"
#   hostname = "contoso-staging.azure-devices.net"
# }
