package module

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

func TestSchemas_ValidateImplementation(t *testing.T) {
	ctx := context.Background()

	var rs resource.SchemaResponse
	NewResource().Schema(ctx, resource.SchemaRequest{}, &rs)
	if diags := rs.Schema.ValidateImplementation(ctx); diags.HasError() {
		t.Fatalf("resource schema implementation: %v", diags)
	}
	for _, name := range []string{"id", "hostname", "device_id", "module_id", "managed_by", "authentication",
		"primary_key_wo", "primary_key_wo_version", "secondary_key_wo", "secondary_key_wo_version", "etag", "generation_id",
		"connection_state", "connection_state_updated_time", "last_activity_time", "cloud_to_device_message_count"} {
		if _, ok := rs.Schema.Attributes[name]; !ok {
			t.Errorf("resource schema is missing %q", name)
		}
	}
	for _, absent := range []string{"status", "status_reason", "edge_enabled", "parent_scope", "device_scope"} {
		if _, ok := rs.Schema.Attributes[absent]; ok {
			t.Errorf("modules have no %q", absent)
		}
	}
	if !rs.Schema.Attributes["primary_key_wo"].IsWriteOnly() || !rs.Schema.Attributes["primary_key_wo"].IsSensitive() {
		t.Error("primary_key_wo must be write-only and sensitive")
	}

	for name, ds := range map[string]datasource.DataSource{"module": NewDataSource(), "modules": NewModulesDataSource()} {
		var s datasource.SchemaResponse
		ds.Schema(ctx, datasource.SchemaRequest{}, &s)
		if diags := s.Schema.ValidateImplementation(ctx); diags.HasError() {
			t.Fatalf("%s data source schema implementation: %v", name, diags)
		}
		for _, forbidden := range []string{"primary_key", "secondary_key"} {
			if _, ok := s.Schema.Attributes[forbidden]; ok {
				t.Errorf("%s data source must not expose %q", name, forbidden)
			}
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

func TestNotSystemModulePattern(t *testing.T) {
	if notSystemModulePattern.MatchString("$edgeAgent") || !notSystemModulePattern.MatchString("telemetry") || !notSystemModulePattern.MatchString("a$b") {
		t.Error("pattern")
	}
}
