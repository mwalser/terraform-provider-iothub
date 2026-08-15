# terraform-provider-iothub

A Terraform provider for the **Azure IoT Hub data plane** — the identity
registry, device and module twins, automatic device management
configurations, IoT Edge deployments, jobs and direct methods exposed by the
IoT Hub *Service REST API*. The hub itself, its routing, endpoints,
certificates and shared access policies are Azure Resource Manager
resources and stay with the [`azurerm`](https://registry.terraform.io/providers/hashicorp/azurerm)
provider; this provider starts exactly where `azurerm` stops.

> **Status: pre-alpha.** Phase 0 (provider skeleton, authentication, client,
> `iothub_device`) is under construction. Nothing is published yet.

The design — every resource, action and behaviour, the decisions behind
them, and the facts verified against a live hub — is in
[`CONCEPT.md`](CONCEPT.md).

## Requirements

- [Terraform](https://developer.hashicorp.com/terraform/downloads) ≥ 1.14
- [Go](https://golang.org/doc/install) ≥ 1.25 (to build)
- An IoT Hub in the Azure public cloud and an identity with an IoT Hub
  data-plane role (e.g. *IoT Hub Data Contributor*) — or a shared access
  policy connection string.

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

```hcl
terraform {
  required_providers {
    iothub = { source = "mwalser/iothub" }
  }
}

provider "iothub" {
  hostname = "contoso-prod.azure-devices.net" # Entra ID via ARM_*/AZURE_* env, MSI or `az login`
}
```

## Developing

```sh
make fmt        # gofmt
make lint       # golangci-lint
make test       # unit tests
make generate   # regenerate docs/ from the schema, examples/ and templates/
```

`docs/` is generated — edit `templates/`, `examples/` and the schema
descriptions instead. CI fails when `docs/` is stale.

### Acceptance tests

Acceptance tests run against a real hub (the F1 free tier is sufficient):

```sh
export TF_ACC=1
export IOTHUB_TEST_HOSTNAME=<hub>.azure-devices.net
# credentials: an `az login` session, ARM_* variables, or IOTHUB_CONNECTION_STRING
make testacc
```

They create and delete devices, twins, configurations and jobs on that hub;
use a hub dedicated to testing.

## License

[MPL-2.0](LICENSE)
