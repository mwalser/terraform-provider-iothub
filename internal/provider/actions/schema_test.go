package actions

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/action"
)

func TestSchemas_ValidateImplementation(t *testing.T) {
	ctx := context.Background()
	for name, a := range map[string]action.Action{
		"direct_method": NewDirectMethodAction(), "set_edge_modules": NewSetEdgeModulesAction(), "purge": NewPurgeC2DQueueAction(),
		"scheduled_job": NewScheduledJobAction(), "import_export_job": NewImportExportJobAction(), "cancel_job": NewCancelJobAction(),
	} {
		var resp action.SchemaResponse
		a.Schema(ctx, action.SchemaRequest{}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("%s: %v", name, resp.Diagnostics)
		}
		if diags := resp.Schema.ValidateImplementation(ctx); diags.HasError() {
			t.Fatalf("%s schema implementation: %v", name, diags)
		}
		if _, ok := resp.Schema.Attributes["hostname"]; ok {
			t.Errorf("%s: the hub is configured on the provider block, not per action", name)
		}
	}
	// Terraform 1.15 rejects write-only attributes in action configuration
	// ("WriteOnly Attribute Not Allowed"), so the container URIs are plain.
	var ie action.SchemaResponse
	NewImportExportJobAction().Schema(ctx, action.SchemaRequest{}, &ie)
	if ie.Schema.Attributes["output_blob_container_uri"].IsWriteOnly() {
		t.Error("action attributes must not be write-only")
	}
}

func TestJobHelpers(t *testing.T) {
	id := newJobID("tf")
	if len(id) != 3+16 || !scheduledJobIDPattern.MatchString(id) {
		t.Errorf("job id %q", id)
	}
	for _, ok := range []string{"fw-channel-1-4-0", "123", "-lead", "trail-"} {
		if !scheduledJobIDPattern.MatchString(ok) {
			t.Errorf("%q should be a valid job ID", ok)
		}
	}
	for _, bad := range []string{"", "Upper", "with_underscore", "with.dot", "with space", strings.Repeat("a", 65)} {
		if scheduledJobIDPattern.MatchString(bad) {
			t.Errorf("%q should not be a valid job ID", bad)
		}
	}
}
