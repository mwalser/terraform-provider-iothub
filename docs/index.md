---
page_title: "iothub Provider"
description: |-
  Manages the Azure IoT Hub data plane: device and module identities, twins, automatic device management configurations, IoT Edge deployments, jobs, direct methods and Plug and Play. The hub itself, and everything else under Azure Resource Manager, is managed with the azurerm provider.
  Requirements:
  Terraform 1.14 or later.An IoT Hub in the Azure public cloud. Sovereign clouds are not supported.Either an Entra ID identity with an IoT Hub data-plane role on the hub, or a shared access policy connection string (see Permissions below).
  Not covered: sending cloud-to-device messages, receiving feedback or file-upload notifications, file upload, and Device Provisioning Service enrollments.
  Authentication is Microsoft Entra ID by default, with the same arguments and environment variables as the azurerm provider. Setting connection_string switches to SAS authentication with a hub shared access policy. Throttled requests are retried automatically until the operation's timeout. The provider is at version 0.x: minor releases may still change attribute names and behaviour, and the changelog lists every such change.
---

# iothub Provider

Manages the Azure IoT Hub **data plane**: device and module identities, twins, automatic device management configurations, IoT Edge deployments, jobs, direct methods and Plug and Play. The hub itself, and everything else under Azure Resource Manager, is managed with the `azurerm` provider.

Requirements:

- Terraform 1.14 or later.
- An IoT Hub in the Azure public cloud. Sovereign clouds are not supported.
- Either an Entra ID identity with an IoT Hub data-plane role on the hub, or a shared access policy connection string (see Permissions below).

Not covered: sending cloud-to-device messages, receiving feedback or file-upload notifications, file upload, and Device Provisioning Service enrollments.

Authentication is Microsoft Entra ID by default, with the same arguments and environment variables as the `azurerm` provider. Setting `connection_string` switches to SAS authentication with a hub shared access policy. Throttled requests are retried automatically until the operation's timeout. The provider is at version 0.x: minor releases may still change attribute names and behaviour, and the changelog lists every such change.

## Example Usage

```terraform
terraform {
  required_version = ">= 1.14"
  required_providers {
    iothub = {
      source  = "mwalser/iothub"
      version = "~> 0.1"
    }
  }
}

# Entra ID (default), configured like the azurerm provider: a client secret or
# certificate, an OIDC token (use_oidc), a managed identity (use_msi) or the
# Azure CLI login, from the block or the ARM_* environment variables. The
# identity needs an IoT Hub data-plane role on the hub, for example "IoT Hub
# Data Contributor". Owner and Contributor are not enough.
provider "iothub" {
  hostname = "contoso-prod.azure-devices.net"
}

# Shared access policy (SAS) instead of Entra ID:
#
# provider "iothub" {
#   connection_string = azurerm_iothub_shared_access_policy.terraform.primary_connection_string
# }

# A second hub: give its provider block an alias and select it on the
# resource with `provider = iothub.staging`.
#
# provider "iothub" {
#   alias    = "staging"
#   hostname = "contoso-staging.azure-devices.net"
# }
```

## Authentication

| Mode | Selected when | Notes |
|---|---|---|
| Microsoft Entra ID (default) | no `connection_string` | The first configured method is used, in this order: client certificate, client secret, OIDC federated token (`use_oidc`), managed identity (`use_msi`), Azure CLI login (`use_cli`, on by default). Arguments and `ARM_*` environment variables are the same as in the `azurerm` provider. The identity needs an IoT Hub **data-plane** role at hub scope. `Owner` and `Contributor` are not enough. |
| Shared access policy (SAS) | `connection_string` set | Uses the hub shared access policy in the connection string. |

### Permissions

The simplest setup is *IoT Hub Data Contributor* (Entra ID) or the `iothubowner`
policy (SAS). Narrower permissions work for subsets:

| To use | Entra ID role | SAS policy permissions |
|---|---|---|
| `iothub_device`, `iothub_module`, their data sources, `iothub_device_credentials`, `iothub_module_credentials` and `iothub_sas_token` | *IoT Hub Registry Contributor*, or *Data Reader* for read-only use | RegistryRead, RegistryWrite |
| Twins (`iothub_*_twin`), `iothub_digital_twin`, `iothub_query`, the `iothub_device` and `iothub_module` list resources | *IoT Hub Twin Contributor*, or *Data Reader* for read-only use | ServiceConnect |
| Configurations and deployments (resources, data sources, list resources), jobs, direct methods, queue purge, statistics | *IoT Hub Data Contributor* | RegistryRead, RegistryWrite, ServiceConnect |

The identity that runs `terraform plan` needs the read permissions too:
refresh reads every managed object, and ephemeral resources open during plan.

One service restriction depends on the authentication mode: with SAS
authentication the hub refuses to create, change or delete modules of a
*disabled* device. Enable the device first, or use Entra ID.

## Hubs

One provider block manages one hub. To manage several hubs, declare one
provider block per hub with an `alias` and select it on the resource with
`provider = iothub.<alias>`. Write the hostname in lowercase, for example
`contoso.azure-devices.net`. Other spellings are rejected.

The hub can be created in the same configuration. `hostname =
azurerm_iothub.x.hostname` is not known during the first plan. The provider
then plans its resources without contacting the hub, and the apply reaches
the hub once it exists. Data sources cannot wait for the apply, so on that
first run they report an error. Apply the hub first, or keep data sources in
a configuration that runs after the hub exists.

<!-- schema generated by tfplugindocs -->
## Schema

### Optional

- `ado_pipeline_service_connection_id` (String) Azure DevOps service connection ID for workload identity federation. Falls back to `ARM_ADO_PIPELINE_SERVICE_CONNECTION_ID` or `ARM_OIDC_AZURE_SERVICE_CONNECTION_ID`.
- `client_certificate` (String, Sensitive) The client certificate itself, base64 encoded (PEM or PKCS#12), instead of a file. Falls back to `ARM_CLIENT_CERTIFICATE`.
- `client_certificate_password` (String, Sensitive) Password of the client certificate, if any. Falls back to `ARM_CLIENT_CERTIFICATE_PASSWORD` or `AZURE_CLIENT_CERTIFICATE_PASSWORD`.
- `client_certificate_path` (String) Path to a PEM or PKCS#12 client certificate for service-principal authentication. Falls back to `ARM_CLIENT_CERTIFICATE_PATH` or `AZURE_CLIENT_CERTIFICATE_PATH`.
- `client_id` (String) Entra ID application (client) ID. Falls back to `ARM_CLIENT_ID` or `AZURE_CLIENT_ID`.
- `client_secret` (String, Sensitive) Client secret for service-principal authentication. Falls back to `ARM_CLIENT_SECRET` or `AZURE_CLIENT_SECRET`.
- `connection_string` (String, Sensitive) Connection string of a hub shared access policy (`HostName=…;SharedAccessKeyName=…;SharedAccessKey=…`). Setting it selects SAS authentication instead of Entra ID. Falls back to `IOTHUB_CONNECTION_STRING`. `azurerm_iothub_shared_access_policy` exposes it as `primary_connection_string`.
- `hostname` (String) Hostname of the IoT Hub, in lowercase, for example `contoso.azure-devices.net`. Falls back to `IOTHUB_HOSTNAME`. When `connection_string` is set you can omit `hostname`, which is then taken from the connection string. If you set both, they must name the same hub. To manage several hubs, declare one provider block per hub with an `alias`.
- `oidc_request_token` (String, Sensitive) Bearer token for `oidc_request_url`, or the system access token of an Azure DevOps pipeline. Falls back to `ARM_OIDC_REQUEST_TOKEN`, `ACTIONS_ID_TOKEN_REQUEST_TOKEN` or `SYSTEM_ACCESSTOKEN`.
- `oidc_request_url` (String) URL of a token request endpoint such as the one GitHub Actions provides. Falls back to `ARM_OIDC_REQUEST_URL` or `ACTIONS_ID_TOKEN_REQUEST_URL`.
- `oidc_token` (String, Sensitive) The federated token itself. Falls back to `ARM_OIDC_TOKEN`.
- `oidc_token_file_path` (String) File containing the federated token, read on every request. Falls back to `ARM_OIDC_TOKEN_FILE_PATH` or `AZURE_FEDERATED_TOKEN_FILE`.
- `tenant_id` (String) Entra ID tenant. Falls back to `ARM_TENANT_ID` or `AZURE_TENANT_ID`.
- `use_cli` (Boolean) Allow the Azure CLI login as the authentication method when no other method is configured (default `true`). Set it to `false` in CI to make a missing configuration fail instead of using a developer's login. Falls back to `ARM_USE_CLI`.
- `use_msi` (Boolean) Authenticate with the managed identity of the machine running Terraform (default `false`). Set `client_id` for a user-assigned identity. Falls back to `ARM_USE_MSI`.
- `use_oidc` (Boolean) Authenticate with a federated (OIDC) token, as HCP Terraform, GitHub Actions, Azure DevOps and Kubernetes workload identity provide it. The token comes from `oidc_token`, `oidc_token_file_path`, or `oidc_request_url` and `oidc_request_token`, in that order; Azure DevOps uses `ado_pipeline_service_connection_id`. Falls back to `ARM_USE_OIDC`.
