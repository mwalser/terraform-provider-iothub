package provider

import "github.com/hashicorp/terraform-plugin-framework/path"

// pathRoot is a small readability helper for attribute-scoped diagnostics.
func pathRoot(name string) path.Path { return path.Root(name) }
