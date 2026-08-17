# terraform-provider-iothub

A Terraform provider for the **Azure IoT Hub data plane**: the identity
registry, device and module twins, automatic device management
configurations, IoT Edge deployments, jobs, direct methods and Plug and Play.
The hub itself, its routing, endpoints, certificates and shared access
policies are Azure Resource Manager resources. They can be managed with the
[`azurerm`](https://registry.terraform.io/providers/hashicorp/azurerm)
provider.

> [!NOTE]
> This provider was created with an LLM.
> I shaped the design, set the feature scope and steered the project until I was happy with the result.
> My hope is that you find this provider useful.

What it manages:

- **Identities** — `iothub_device` and `iothub_module` as resources, data
  sources and `terraform query` list resources. Credentials and device SAS
  tokens as ephemeral resources.
- **Twins** — `iothub_device_twin` and `iothub_module_twin`. Terraform manages
  exactly the keys you declare and leaves the rest of the twin alone. Reported
  properties and the Plug and Play digital twin as data sources.
- **Configurations** — `iothub_configuration` for automatic device management
  and `iothub_edge_deployment` for IoT Edge, including layered deployments;
  `provider::iothub::edge_manifest` builds deployment manifests from HCL.
- **Actions** — direct methods (including Plug and Play commands), scheduled
  twin and method jobs, bulk import and export, setting the modules of one
  edge device, purging a device's cloud-to-device queue, cancelling jobs.
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

# Entra ID like azurerm: ARM_* variables, use_oidc, use_msi or `az login`.
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

# Manage exactly these keys of the twin. Anything else in it is left alone.
# A changed twin triggers the reboot action below.
resource "iothub_device_twin" "sensor" {
  device_id = iothub_device.sensor.device_id
  tags      = jsonencode({ site = "munich", fleet = { ring = 2 } })
  desired_properties = jsonencode({
    telemetryIntervalSec = 60
    firmware             = { channel = "stable" }
  })

  lifecycle {
    action_trigger {
      events  = [after_update]
      actions = [action.iothub_direct_method.reboot]
    }
  }
}

# Set the maintenance window for the whole EU fleet. Configurations and twin
# resources should not write the same desired property.
resource "iothub_configuration" "maintenance" {
  configuration_id = "maintenance-window-eu"
  target_condition = "tags.fleet.region = 'eu'"
  priority         = 10
  device_content   = jsonencode({ "properties.desired.maintenance" = { window = "02:00-04:00" } })
}

action "iothub_direct_method" "reboot" {
  config {
    device_id   = iothub_device.sensor.device_id
    method_name = "reboot"
  }
}

# The connection string reaches Key Vault through a write-only argument
# without passing through state.
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

One provider block manages one hub. Use provider aliases for several hubs.
The hub can be created in the same configuration: `hostname =
azurerm_iothub.x.hostname` works, and the provider addresses the hub once it
exists.

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

In CI, the `Tests` workflow runs the suite on pushes to `main`, on release
tags and on demand; the variables and secrets it needs are listed at the top
of `.github/workflows/test.yml`.

## Releasing

Push a tag `vX.Y.Z`. The `Release` workflow runs the tests on that commit,
then builds, signs and publishes the release with GoReleaser. Bump
`CHANGELOG.md` before tagging.

## License

[MPL-2.0](LICENSE)
