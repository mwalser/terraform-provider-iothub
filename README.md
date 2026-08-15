# terraform-provider-iothub

A Terraform provider for the **Azure IoT Hub data plane**: the identity
registry, device and module twins, automatic device management
configurations, IoT Edge deployments, jobs, direct methods and Plug and Play.
The hub itself, its routing, endpoints, certificates and shared access
policies are Azure Resource Manager resources. They stay with the
[`azurerm`](https://registry.terraform.io/providers/hashicorp/azurerm)
provider. This provider starts where `azurerm` stops.

> **Status: alpha, not yet published to the Terraform Registry.** Feature
> complete for the scope below, but not yet released.

What it manages:

- **Identities** — `iothub_device` and `iothub_module` as resources, data
  sources and `terraform query` list resources. Credentials and device SAS
  tokens as ephemeral resources.
- **Twins** — `iothub_device_twin` and `iothub_module_twin`. Terraform manages
  exactly the keys you declare and leaves the rest of the twin alone. Reported
  properties and the Plug and Play digital twin as data sources.
- **Configurations** — `iothub_configuration` for automatic device management
  and `iothub_edge_deployment` for IoT Edge, including layered deployments.
- **Actions** — direct methods, Plug and Play commands, scheduled twin and
  method jobs, bulk import and export, applying a manifest to one edge device,
  purging a device's cloud-to-device queue, cancelling jobs.
- **Data sources** — `iothub_query` for the IoT Hub query language,
  `iothub_statistics`, job status.

Reference documentation is generated into [`docs/`](docs/). The design and
the service behaviour it is built on are in [`CONCEPT.md`](CONCEPT.md), which
is written for maintainers. See [`CONTRIBUTING.md`](CONTRIBUTING.md) for
contributions.

## Requirements

- [Terraform](https://developer.hashicorp.com/terraform/downloads) ≥ 1.14
- [Go](https://golang.org/doc/install) ≥ 1.25 (to build)
- An IoT Hub in the Azure public cloud, plus either an identity with an IoT
  Hub data-plane role at hub scope, for example *IoT Hub Data Contributor*, or
  a shared access policy connection string. `Owner` and `Contributor` carry no
  data-plane permissions.

## Usage

```hcl
terraform {
  required_providers {
    iothub = { source = "mwalser/iothub" }
  }
}

# Entra ID via ARM_*/AZURE_* variables, a workload/managed identity or `az login`.
provider "iothub" {
  hostname = "contoso-prod.azure-devices.net"
}

resource "iothub_device" "gateway" {
  device_id    = "gw-munich-01"
  edge_enabled = true
  authentication = { type = "certificateAuthority" }
}

resource "iothub_device" "sensor" {
  device_id    = "sensor-0001"
  parent_scope = iothub_device.gateway.device_scope
}

# Manage exactly these values of the twin. Anything else in it is left alone.
resource "iothub_device_twin" "sensor" {
  device_id = iothub_device.sensor.device_id
  tags      = jsonencode({ site = "munich", fleet = { ring = 2 } })
  desired_properties = jsonencode({
    telemetryIntervalSec = 60
    firmware             = { channel = "stable" }
  })
}

# Roll a firmware channel out to the whole EU fleet.
resource "iothub_configuration" "fw_channel" {
  configuration_id = "fw-channel-stable"
  target_condition = "tags.fleet.region = 'eu'"
  priority         = 10
  device_content   = jsonencode({ "properties.desired.firmware" = { channel = "stable" } })
}

# Reboot the sensor whenever its twin changes.
action "iothub_direct_method" "reboot" {
  config {
    device_id   = iothub_device.sensor.device_id
    method_name = "reboot"
  }
}

# Keys never touch state: read them at apply time into a write-only argument.
ephemeral "iothub_device_credentials" "sensor" {
  device_id = iothub_device.sensor.device_id
}

resource "azurerm_key_vault_secret" "sensor" {
  name             = "sensor-0001-connection-string"
  key_vault_id     = azurerm_key_vault.devices.id
  value_wo         = ephemeral.iothub_device_credentials.sensor.primary_connection_string
  value_wo_version = 1
}
```

Every resource, data source, ephemeral resource and action accepts its own
`hostname` (lowercase), so one provider block can manage several hubs, and a
hub created in the same configuration (`azurerm_iothub.x.hostname`) can be
referenced before it exists.

## Using a local build

```sh
make build            # or: go build -o ~/go/bin/terraform-provider-iothub .
```

Point Terraform at the binary with a CLI configuration file:

```hcl
# ~/.terraformrc (or any file named by TF_CLI_CONFIG_FILE)
provider_installation {
  dev_overrides {
    "mwalser/iothub" = "/absolute/path/to/directory/containing/the/binary"
  }
  direct {}
}
```

With dev overrides in effect, Terraform skips `terraform init` for this
provider. `plan` and `apply` work directly.

## Developing

```sh
make fmt        # gofmt
make lint       # golangci-lint
make test       # unit tests (no Azure access needed)
make generate   # regenerate docs/ from the schema, examples/ and templates/
```

`docs/` is generated. Edit `templates/`, `examples/` and the schema
descriptions instead. CI fails when `docs/` is stale.

Layout: `internal/client` (framework-free REST client: auth, retry, errors,
operations), `internal/provider` (provider, `common/` shared data,
one package per construct family, acceptance tests as `*_acc_test.go`),
`internal/acctest` (acceptance harness).

### Acceptance tests

Acceptance tests run against a real hub. The F1 free tier is sufficient.
They create and delete devices, twins, configurations and jobs on that hub,
so use a hub dedicated to testing.

```sh
export TF_ACC=1
export IOTHUB_TEST_HOSTNAME=<hub>.azure-devices.net
# credentials: an `az login` session, ARM_* variables, or IOTHUB_CONNECTION_STRING
make testacc                                    # everything
go test ./internal/provider/ -run TestAccDevice -v   # one family
```

The import/export job test additionally needs a blob container the hub can
use. Pass it as a container SAS URI with `racwdl` permissions in
`IOTHUB_TEST_BLOB_CONTAINER_SAS_URI`. The test is skipped otherwise.
Everything else runs against the hub alone.

### Acceptance tests in CI

The `Tests` workflow runs the acceptance suite on `workflow_dispatch`, and on
every push to `main` once the repository **variable** `IOTHUB_TEST_HOSTNAME`
is set. Credentials come from repository **secrets**, either

- `IOTHUB_CONNECTION_STRING` — a shared access policy of the test hub, or
- `AZURE_CLIENT_ID`, `AZURE_TENANT_ID`, `AZURE_SUBSCRIPTION_ID` — an Entra ID
  application with a [federated credential for GitHub Actions](https://learn.microsoft.com/en-us/entra/workload-id/workload-identity-federation-create-trust?pivots=identity-wif-apps-methods-azp#github-actions)
  and *IoT Hub Data Contributor* on the hub. The workflow logs in with
  `azure/login` and the provider uses that CLI session.

Optionally, `IOTHUB_TEST_BLOB_CONTAINER_SAS_URI` enables the import/export
job test.

## Releasing

Releases are cut by pushing a tag `vX.Y.Z`. The `Release` workflow builds
all platforms with GoReleaser, signs the checksums and publishes a GitHub
release. It needs the secrets `GPG_PRIVATE_KEY` and `PASSPHRASE` for the key
whose public part is registered with the Terraform Registry. Bump
`CHANGELOG.md` before tagging.

## License

[MPL-2.0](LICENSE)
