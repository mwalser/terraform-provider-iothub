// Package edgemanifest implements provider::iothub::edge_manifest, which
// builds the modulesContent of an IoT Edge deployment manifest from a typed
// object. Design and the runtime facts it rests on: CONCEPT.md §6.6.
package edgemanifest

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ function.Function = Function{}

// New returns the edge_manifest function.
func New() function.Function { return Function{} }

// Function is provider::iothub::edge_manifest.
type Function struct{}

// Metadata implements function.Function.
func (Function) Metadata(_ context.Context, _ function.MetadataRequest, resp *function.MetadataResponse) {
	resp.Name = "edge_manifest"
}

// Definition implements function.Function.
func (Function) Definition(_ context.Context, _ function.DefinitionRequest, resp *function.DefinitionResponse) {
	resp.Definition = function.Definition{
		Summary:             "Builds the `modules_content` of an IoT Edge deployment from an object.",
		MarkdownDescription: description,
		Parameters: []function.Parameter{
			function.DynamicParameter{
				Name:                "manifest",
				MarkdownDescription: "The manifest as an object with the keys listed above.",
			},
		},
		Return: function.StringReturn{},
	}
}

// Run implements function.Function.
func (Function) Run(ctx context.Context, req function.RunRequest, resp *function.RunResponse) {
	var arg types.Dynamic
	resp.Error = function.ConcatFuncErrors(resp.Error, req.Arguments.Get(ctx, &arg))
	if resp.Error != nil {
		return
	}
	tv, err := arg.ToTerraformValue(ctx)
	if err != nil {
		resp.Error = function.NewArgumentFuncError(0, err.Error())
		return
	}
	v, err := toGo(tv)
	if err != nil {
		resp.Error = function.NewArgumentFuncError(0, err.Error())
		return
	}
	out, problems := Build(v)
	if len(problems) > 0 {
		resp.Error = function.NewArgumentFuncError(0, Format(problems))
		return
	}
	resp.Error = function.ConcatFuncErrors(resp.Error, resp.Result.Set(ctx, types.StringValue(out)))
}

// description is the function's documentation. Rules: CONTRIBUTING.md.
const description = "Builds the `modules_content` for `iothub_edge_deployment` and `iothub_set_edge_modules` " +
	"from an object, so that a deployment manifest is written in HCL.\n\n" +
	"The result is compact JSON with sorted keys, `$edgeAgent` schema version 1.1 and `$edgeHub` schema " +
	"version 1.1.\n\n" +
	"## The `manifest` object\n\n" +
	"| Key | Meaning |\n" +
	"|---|---|\n" +
	"| `edge_agent` | The `edgeAgent` system module: `image` (required), `create_options`, `env`, `image_pull_policy`. Required unless `layered`. |\n" +
	"| `edge_hub` | The `edgeHub` system module: `image` (required), `create_options` (default: port bindings for 5671, 8883 and 443), `env`, `image_pull_policy`, `startup_order`. Required unless `layered`. |\n" +
	"| `registry_credentials` | Container registries the device pulls from, by label: `address`, `username`, `password`. Not allowed when `layered`. |\n" +
	"| `modules` | Custom modules by name (letters, digits, `-`, `_`), see below. |\n" +
	"| `routes` | Routes by name: the route string, or an object with `route`, `priority` (0–9, 0 first) and `time_to_live_secs`. |\n" +
	"| `store_and_forward` | `time_to_live_secs` (default 7200; -1 keeps messages until delivered) and `max_size_bytes`. Not allowed when `layered`. |\n" +
	"| `layered` | `true` builds a layered deployment: `modules` and `routes` are added on top of a base deployment. |\n\n" +
	"A module:\n\n" +
	"| Key | Meaning |\n" +
	"|---|---|\n" +
	"| `image` | Container image reference. Required. |\n" +
	"| `create_options` | Docker container create options as an object or a JSON string. |\n" +
	"| `env` | Environment variables; values are strings, numbers or booleans. |\n" +
	"| `status` | `running` (default) or `stopped`. |\n" +
	"| `restart_policy` | `always` (default), `on-failure`, `on-unhealthy` or `never`. |\n" +
	"| `image_pull_policy` | `on-create` (default) or `never`. |\n" +
	"| `startup_order` | 0 to 4294967295; lower starts earlier. |\n" +
	"| `version` | Free text, kept in the manifest. |\n" +
	"| `desired` | The module twin's desired properties, as an object. |\n"
