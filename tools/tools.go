//go:build generate

// Package tools pins the code-generation tooling (docs) without adding it to
// the provider's own dependency graph.
package tools

import (
	_ "github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs"
)

// Format the Terraform examples that feed the generated documentation.
//go:generate terraform fmt -recursive ../examples/

// Generate the registry documentation under ../docs from the provider schema,
// ../examples and ../templates.
//go:generate go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs generate --provider-dir .. -provider-name iothub -rendered-provider-name "Azure IoT Hub"
