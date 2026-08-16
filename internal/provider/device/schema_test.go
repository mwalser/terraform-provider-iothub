package device

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// The framework validates schema rules (write-only + computed, defaults on
// computed, plan modifiers) at runtime; run those checks in unit tests so a
// broken schema fails fast, not on first `terraform plan`.
func TestSchemas_ValidateImplementation(t *testing.T) {
	ctx := context.Background()

	var rs resource.SchemaResponse
	NewResource().Schema(ctx, resource.SchemaRequest{}, &rs)
	if rs.Diagnostics.HasError() {
		t.Fatalf("resource schema: %v", rs.Diagnostics)
	}
	if diags := rs.Schema.ValidateImplementation(ctx); diags.HasError() {
		t.Fatalf("resource schema implementation: %v", diags)
	}
	for _, name := range []string{"id", "device_id", "status", "status_reason", "edge_enabled", "parent_scope", "authentication",
		"primary_key_wo", "primary_key_wo_version", "secondary_key_wo", "secondary_key_wo_version", "etag", "generation_id", "device_scope",
		"connection_state", "connection_state_updated_time", "last_activity_time", "status_updated_time", "cloud_to_device_message_count"} {
		if _, ok := rs.Schema.Attributes[name]; !ok {
			t.Errorf("resource schema is missing %q", name)
		}
	}
	if len(rs.Schema.Blocks) != 0 {
		t.Error("resource schema must not declare blocks (no timeouts block: throttling retries have a fixed budget)")
	}
	if !rs.Schema.Attributes["primary_key_wo"].IsWriteOnly() || !rs.Schema.Attributes["primary_key_wo"].IsSensitive() {
		t.Error("primary_key_wo must be write-only and sensitive")
	}

	var ds datasource.SchemaResponse
	NewDataSource().Schema(ctx, datasource.SchemaRequest{}, &ds)
	if diags := ds.Schema.ValidateImplementation(ctx); diags.HasError() {
		t.Fatalf("data source schema implementation: %v", diags)
	}
	for _, forbidden := range []string{"primary_key", "secondary_key"} {
		if _, ok := ds.Schema.Attributes[forbidden]; ok {
			t.Errorf("data source must not expose %q", forbidden)
		}
	}

	var es ephemeral.SchemaResponse
	NewCredentialsEphemeral().Schema(ctx, ephemeral.SchemaRequest{}, &es)
	if diags := es.Schema.ValidateImplementation(ctx); diags.HasError() {
		t.Fatalf("ephemeral schema implementation: %v", diags)
	}
	for _, name := range []string{"primary_key", "secondary_key", "primary_connection_string", "secondary_connection_string"} {
		if !es.Schema.Attributes[name].IsSensitive() {
			t.Errorf("ephemeral attribute %q must be sensitive", name)
		}
	}
}

func TestSASTokenSchema_ValidateImplementation(t *testing.T) {
	ctx := context.Background()
	var es ephemeral.SchemaResponse
	NewSASTokenEphemeral().Schema(ctx, ephemeral.SchemaRequest{}, &es)
	if diags := es.Schema.ValidateImplementation(ctx); diags.HasError() {
		t.Fatalf("schema: %v", diags)
	}
	if !es.Schema.Attributes["token"].IsSensitive() {
		t.Error("token must be sensitive")
	}
}
