# terraform-provider-iothub

A Terraform provider for the **Azure IoT Hub data plane** — the identity
registry, device and module twins, automatic device management
configurations, IoT Edge deployments, jobs and direct methods exposed by the
IoT Hub *Service REST API*. The hub itself, its routing, endpoints,
certificates and shared access policies are Azure Resource Manager
resources and stay with the [`azurerm`](https://registry.terraform.io/providers/hashicorp/azurerm)
provider; this provider starts exactly where `azurerm` stops.

> **Status: alpha, not yet published to the Terraform Registry.**
> Phase 0 is complete: authentication, the service client, `iothub_device`
> (resource, data source, import), `iothub_device_credentials` and
> `iothub_statistics`. Modules, twins, configurations, edge deployments and
> actions follow ([roadmap](CONCEPT.md#14-roadmap)).

The design — every resource, action and behaviour, the decisions behind
them, and the service facts verified against a live hub — is in
[`CONCEPT.md`](CONCEPT.md). Generated reference documentation lives in
[`docs/`](docs/).

## Requirements

- [Terraform](https://developer.hashicorp.com/terraform/downloads) ≥ 1.14
- [Go](https://golang.org/doc/install) ≥ 1.25 (to build)
- An IoT Hub in the Azure public cloud and an identity with an IoT Hub
  data-plane role at hub scope (e.g. *IoT Hub Data Contributor*; `Owner` and
  `Contributor` carry no data-plane permissions) — or a shared access policy
  connection string.

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

Every resource, data source and ephemeral resource accepts its own
`hostname`, so one provider block can manage several hubs, and a hub created
in the same configuration (`azurerm_iothub.x.hostname`) can be referenced
before it exists.

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

With dev overrides in effect Terraform skips `terraform init` for this
provider; `plan`/`apply` work directly.

## Developing

```sh
make fmt        # gofmt
make lint       # golangci-lint
make test       # unit tests (no Azure access needed)
make generate   # regenerate docs/ from the schema, examples/ and templates/
```

`docs/` is generated — edit `templates/`, `examples/` and the schema
descriptions instead. CI fails when `docs/` is stale.

Layout: `internal/client` (framework-free REST client: auth, retry, errors,
operations), `internal/provider` (provider, `common/` shared data,
one package per construct family, acceptance tests as `*_acc_test.go`),
`internal/acctest` (acceptance harness).

### Acceptance tests

Acceptance tests run against a real hub — the F1 free tier is sufficient.
They create and delete devices, twins, configurations and jobs on that hub,
so use a hub dedicated to testing.

```sh
export TF_ACC=1
export IOTHUB_TEST_HOSTNAME=<hub>.azure-devices.net
# credentials: an `az login` session, ARM_* variables, or IOTHUB_CONNECTION_STRING
make testacc                                    # everything
go test ./internal/provider/ -run TestAccDevice -v   # one family
```

### Acceptance tests in CI

The `Tests` workflow runs the acceptance suite on `workflow_dispatch`, and on
every push to `main` once the repository **variable** `IOTHUB_TEST_HOSTNAME`
is set. Credentials come from repository **secrets**, either

- `IOTHUB_CONNECTION_STRING` — a shared access policy of the test hub, or
- `AZURE_CLIENT_ID`, `AZURE_TENANT_ID`, `AZURE_SUBSCRIPTION_ID` — an Entra ID
  application with a [federated credential for GitHub Actions](https://learn.microsoft.com/en-us/entra/workload-id/workload-identity-federation-create-trust?pivots=identity-wif-apps-methods-azp#github-actions)
  and *IoT Hub Data Contributor* on the hub; the workflow logs in with
  `azure/login` and the provider uses that CLI session.

## Releasing

Releases are cut by pushing a tag `vX.Y.Z`; the `Release` workflow builds
all platforms with GoReleaser, signs the checksums and publishes a GitHub
release. It needs the secrets `GPG_PRIVATE_KEY` and `PASSPHRASE` (the key
whose public part is registered with the Terraform Registry). Bump
`CHANGELOG.md` before tagging.

## License

[MPL-2.0](LICENSE)
