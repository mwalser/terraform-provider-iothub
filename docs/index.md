---
page_title: "iothub Provider"
description: |-
  Manages the Azure IoT Hub data plane: device and module identities, twins, automatic device management configurations, IoT Edge deployments, jobs, direct methods and Plug and Play. The hub itself, and everything else under Azure Resource Manager, is managed with the azurerm provider.
  Requirements:
  Terraform 1.14 or later.An IoT Hub in the Azure public cloud. Sovereign clouds are not supported.Either an Entra ID identity with an IoT Hub data-plane role on the hub, or a shared access policy connection string (see Permissions below).
  Not covered: sending cloud-to-device messages, receiving feedback or file-upload notifications, file upload, and Device Provisioning Service enrollments.
  Authentication is Microsoft Entra ID by default, with the argument names and ARM_* variables of the azurerm provider. Setting connection_string switches to SAS authentication with a hub shared access policy. Throttled requests are retried automatically for up to 20 minutes per request; actions bound their whole invocation with timeout. The provider is at version 0.x: minor releases may still change attribute names and behaviour, and the changelog lists every such change.
---

# iothub Provider

Manages the Azure IoT Hub **data plane**: device and module identities, twins, automatic device management configurations, IoT Edge deployments, jobs, direct methods and Plug and Play. The hub itself, and everything else under Azure Resource Manager, is managed with the `azurerm` provider.

Requirements:

- Terraform 1.14 or later.
- An IoT Hub in the Azure public cloud. Sovereign clouds are not supported.
- Either an Entra ID identity with an IoT Hub data-plane role on the hub, or a shared access policy connection string (see Permissions below).

Not covered: sending cloud-to-device messages, receiving feedback or file-upload notifications, file upload, and Device Provisioning Service enrollments.

Authentication is Microsoft Entra ID by default, with the argument names and `ARM_*` variables of the `azurerm` provider. Setting `connection_string` switches to SAS authentication with a hub shared access policy. Throttled requests are retried automatically for up to 20 minutes per request; actions bound their whole invocation with `timeout`. The provider is at version 0.x: minor releases may still change attribute names and behaviour, and the changelog lists every such change.

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

The provider authenticates with Microsoft Entra ID unless `connection_string`
is set, which selects a hub shared access policy (SAS) instead. Entra ID uses
the argument names and `ARM_*` environment variables of the `azurerm`
provider, plus the `AZURE_*` variables the Azure SDK reads, so one
environment serves both providers. The identity needs an IoT Hub
**data-plane** role at hub scope (see Permissions). `Owner` and `Contributor`
are not enough.

Three rules:

- An argument in the provider block wins over `ARM_*` variables, which win
  over `AZURE_*` variables.
- The first method whose inputs are present is used, in the order of the
  table below. Nothing is probed or guessed.
- A method that is switched on but incomplete is an error. It does not fall
  back to the Azure CLI. Set `use_cli = false` in CI so that a missing
  configuration fails instead of using a developer's login.

| Method | Set | Also needed | Environment |
|---|---|---|---|
| Client certificate | `client_certificate_path` or `client_certificate` (base64) | `tenant_id`, `client_id`, `client_certificate_password` if the file has one | `ARM_CLIENT_CERTIFICATE_PATH`, `ARM_CLIENT_CERTIFICATE`, `ARM_CLIENT_CERTIFICATE_PASSWORD` |
| Client secret | `client_secret` or `client_secret_file_path` | `tenant_id`, `client_id` (or `client_id_file_path`) | `ARM_CLIENT_SECRET`, `ARM_CLIENT_SECRET_FILE_PATH` |
| OIDC: HCP Terraform | `use_oidc = true` | `tenant_id`; HCP sets `client_id` and `oidc_token` | `ARM_USE_OIDC`, `ARM_OIDC_TOKEN`, `ARM_CLIENT_ID` (set by HCP), `ARM_TENANT_ID` (yours); several provider configurations use `client_id_file_path` and `oidc_token_file_path` |
| OIDC: GitHub Actions | `use_oidc = true` | `tenant_id`, `client_id`; the job needs `permissions: id-token: write` | `ARM_USE_OIDC`, `ARM_CLIENT_ID`, `ARM_TENANT_ID`; the request URL and token come from `ACTIONS_ID_TOKEN_REQUEST_URL` and `ACTIONS_ID_TOKEN_REQUEST_TOKEN` |
| OIDC: Azure DevOps | `use_oidc = true`, `ado_pipeline_service_connection_id` | `tenant_id`, `client_id`; export `SYSTEM_ACCESSTOKEN: $(System.AccessToken)` and `SYSTEM_OIDCREQUESTURI: $(System.OidcRequestUri)` in the task | `ARM_USE_OIDC`, `ARM_ADO_PIPELINE_SERVICE_CONNECTION_ID`, `SYSTEM_ACCESSTOKEN`, `SYSTEM_OIDCREQUESTURI` |
| OIDC: AKS workload identity | `use_oidc = true` (or azurerm's `use_aks_workload_identity = true`) | nothing else; AKS injects the client ID, tenant ID and token file | `ARM_USE_OIDC` or `ARM_USE_AKS_WORKLOAD_IDENTITY`; `AZURE_CLIENT_ID`, `AZURE_TENANT_ID`, `AZURE_FEDERATED_TOKEN_FILE` |
| Managed identity | `use_msi = true` | `client_id` for a user-assigned identity; the endpoint is found automatically on VMs, App Service, Container Apps and Arc | `ARM_USE_MSI` |
| Azure CLI | nothing (`use_cli` defaults to `true`) | an `az login` session | `ARM_USE_CLI=false` disables it |
| Shared access policy | `connection_string` | nothing; `hostname` is taken from it | `IOTHUB_CONNECTION_STRING` |

Two short examples. A GitHub Actions job:

```yaml
permissions:
  id-token: write
  contents: read
env:
  ARM_USE_OIDC: "true"
  ARM_CLIENT_ID: <application (client) ID>
  ARM_TENANT_ID: <tenant ID>
```

An Azure DevOps task:

```yaml
- task: AzureCLI@2
  env:
    ARM_USE_OIDC: "true"
    ARM_ADO_PIPELINE_SERVICE_CONNECTION_ID: <service connection ID>
    SYSTEM_ACCESSTOKEN: $(System.AccessToken)
    SYSTEM_OIDCREQUESTURI: $(System.OidcRequestUri)
```

The Azure side (app registration, federated credential, role assignment) is
the same as for `azurerm`; its
[authentication guides](https://registry.terraform.io/providers/hashicorp/azurerm/latest/docs/guides/service_principal_oidc)
apply, with one difference: assign an IoT Hub data role, not *Contributor*.

`azurerm` arguments with no equivalent here, because the data plane needs
none of them: `subscription_id`, `environment` (public cloud only),
`metadata_host`, `auxiliary_tenant_ids`, `msi_endpoint`, `msi_api_version`,
`partner_id`, `storage_use_azuread`, `resource_provider_registrations` and
`features`.

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
- `client_certificate` (String, Sensitive) The client certificate itself, base64 encoded (PKCS#12 or PEM), instead of a file. Falls back to `ARM_CLIENT_CERTIFICATE`.
- `client_certificate_password` (String, Sensitive) Password of the client certificate, if any. Falls back to `ARM_CLIENT_CERTIFICATE_PASSWORD` or `AZURE_CLIENT_CERTIFICATE_PASSWORD`.
- `client_certificate_path` (String) Path to the client certificate for service-principal authentication: a PKCS#12 file (`.pfx`, as `openssl pkcs12 -export` writes it) or a PEM bundle with the certificate and an unencrypted private key. Falls back to `ARM_CLIENT_CERTIFICATE_PATH` or `AZURE_CLIENT_CERTIFICATE_PATH`.
- `client_id` (String) Entra ID application (client) ID. Falls back to `ARM_CLIENT_ID` or `AZURE_CLIENT_ID`.
- `client_id_file_path` (String) File containing the client ID, as HCP Terraform provides it for multiple provider configurations. Falls back to `ARM_CLIENT_ID_FILE_PATH`.
- `client_secret` (String, Sensitive) Client secret for service-principal authentication. Falls back to `ARM_CLIENT_SECRET` or `AZURE_CLIENT_SECRET`.
- `client_secret_file_path` (String) File containing the client secret, for mounted secrets. Falls back to `ARM_CLIENT_SECRET_FILE_PATH`.
- `connection_string` (String, Sensitive) Connection string of a hub shared access policy (`HostName=…;SharedAccessKeyName=…;SharedAccessKey=…`). Setting it selects SAS authentication instead of Entra ID. Falls back to `IOTHUB_CONNECTION_STRING`. `azurerm_iothub_shared_access_policy` exposes it as `primary_connection_string`.
- `hostname` (String) Hostname of the IoT Hub, in lowercase, for example `contoso.azure-devices.net`. Falls back to `IOTHUB_HOSTNAME`. When `connection_string` is set you can omit `hostname`, which is then taken from the connection string. If you set both, they must name the same hub. To manage several hubs, declare one provider block per hub with an `alias`.
- `oidc_request_token` (String, Sensitive) Bearer token for `oidc_request_url`: the GitHub Actions request token or the Azure Pipelines system access token. Falls back to `ARM_OIDC_REQUEST_TOKEN`, `ACTIONS_ID_TOKEN_REQUEST_TOKEN` or `SYSTEM_ACCESSTOKEN`.
- `oidc_request_url` (String) URL of the token request endpoint of GitHub Actions or Azure Pipelines. Falls back to `ARM_OIDC_REQUEST_URL`, `ACTIONS_ID_TOKEN_REQUEST_URL` or `SYSTEM_OIDCREQUESTURI`.
- `oidc_token` (String, Sensitive) The federated token itself. Falls back to `ARM_OIDC_TOKEN`.
- `oidc_token_file_path` (String) File containing the federated token, read on every request. Falls back to `ARM_OIDC_TOKEN_FILE_PATH` or `AZURE_FEDERATED_TOKEN_FILE`.
- `tenant_id` (String) Entra ID tenant. Falls back to `ARM_TENANT_ID` or `AZURE_TENANT_ID`.
- `use_aks_workload_identity` (Boolean) The same as `use_oidc`, under azurerm's name for AKS workload identity. The `AZURE_CLIENT_ID`, `AZURE_TENANT_ID` and `AZURE_FEDERATED_TOKEN_FILE` variables that AKS injects are read either way. Falls back to `ARM_USE_AKS_WORKLOAD_IDENTITY`.
- `use_cli` (Boolean) Allow the Azure CLI login as the authentication method when no other method is configured (default `true`). Set it to `false` in CI to make a missing configuration fail instead of using a developer's login. Falls back to `ARM_USE_CLI`.
- `use_msi` (Boolean) Authenticate with the managed identity of the machine running Terraform (default `false`). Set `client_id` for a user-assigned identity. Falls back to `ARM_USE_MSI`.
- `use_oidc` (Boolean) Authenticate with a federated (OIDC) token, as HCP Terraform, GitHub Actions, Azure DevOps and Kubernetes workload identity provide it. With `ado_pipeline_service_connection_id` the token comes from Azure DevOps; otherwise from `oidc_token`, `oidc_token_file_path`, or `oidc_request_url` and `oidc_request_token`, in that order. Falls back to `ARM_USE_OIDC`.
