package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
)

// TestProvider_ServesProtocol6 makes sure the provider can be served over
// protocol 6, which is what the acceptance-test factories in later steps use.
func TestProvider_ServesProtocol6(t *testing.T) {
	factory := providerserver.NewProtocol6WithError(New("test")())
	srv, err := factory()
	if err != nil {
		t.Fatalf("protocol 6 server: %v", err)
	}
	if srv == nil {
		t.Fatal("protocol 6 server is nil")
	}
}

func TestProvider_Schema(t *testing.T) {
	ctx := context.Background()
	p := New("test")()

	var resp provider.SchemaResponse
	p.Schema(ctx, provider.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	if diags := resp.Schema.ValidateImplementation(ctx); diags.HasError() {
		t.Fatalf("schema implementation invalid: %v", diags)
	}
	for _, name := range []string{"hostname", "connection_string", "tenant_id", "client_id", "use_oidc", "use_msi", "use_cli"} {
		if _, ok := resp.Schema.Attributes[name]; !ok {
			t.Errorf("provider schema is missing attribute %q", name)
		}
	}
	// No behaviour knobs (CONCEPT.md §14 rows 5, 8, 9).
	for _, forbidden := range []string{"api_version", "optimistic_locking", "registry_ops_per_minute"} {
		if _, ok := resp.Schema.Attributes[forbidden]; ok {
			t.Errorf("provider schema must not expose behaviour knob %q", forbidden)
		}
	}

	var meta provider.MetadataResponse
	p.Metadata(ctx, provider.MetadataRequest{}, &meta)
	if meta.TypeName != "iothub" {
		t.Errorf("type name = %q, want iothub", meta.TypeName)
	}
}

func TestProvider_Functions(t *testing.T) {
	ctx := context.Background()
	p, ok := New("test")().(provider.ProviderWithFunctions)
	if !ok {
		t.Fatal("provider does not serve functions")
	}
	var names []string
	for _, f := range p.Functions(ctx) {
		var meta function.MetadataResponse
		f().Metadata(ctx, function.MetadataRequest{}, &meta)
		names = append(names, meta.Name)
		var def function.DefinitionResponse
		f().Definition(ctx, function.DefinitionRequest{}, &def)
		var valid function.DefinitionValidateResponse
		def.Definition.ValidateImplementation(ctx, function.DefinitionValidateRequest{FuncName: meta.Name}, &valid)
		if valid.Diagnostics.HasError() {
			t.Errorf("function %q: %v", meta.Name, valid.Diagnostics)
		}
	}
	if len(names) != 1 || names[0] != "edge_manifest" {
		t.Errorf("functions = %v, want [edge_manifest]", names)
	}
}
