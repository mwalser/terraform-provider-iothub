package actions

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/action"
)

func TestSchemas_ValidateImplementation(t *testing.T) {
	ctx := context.Background()
	for name, a := range map[string]action.Action{
		"direct_method": NewDirectMethodAction(), "apply_configuration": NewApplyConfigurationAction(), "purge": NewPurgeC2DQueueAction(),
		"scheduled_job": NewScheduledJobAction(), "import_export_job": NewImportExportJobAction(), "cancel_job": NewCancelJobAction(),
		"digital_twin_command": NewDigitalTwinCommandAction(),
	} {
		var resp action.SchemaResponse
		a.Schema(ctx, action.SchemaRequest{}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("%s: %v", name, resp.Diagnostics)
		}
		if diags := resp.Schema.ValidateImplementation(ctx); diags.HasError() {
			t.Fatalf("%s schema implementation: %v", name, diags)
		}
		if _, ok := resp.Schema.Attributes["hostname"]; !ok {
			t.Errorf("%s: missing hostname", name)
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

func TestDTDLNamePattern(t *testing.T) {
	for _, ok := range []string{"reboot", "getMaxMinReport", "thermostat1", "a", "A_b9"} {
		if !dtdlNamePattern.MatchString(ok) {
			t.Errorf("%q should be a valid DTDL name", ok)
		}
	}
	for _, bad := range []string{"", "1abc", "a-b", "a b", "a*b", "trailing_", "$edgeAgent"} {
		if dtdlNamePattern.MatchString(bad) {
			t.Errorf("%q should not be a valid DTDL name", bad)
		}
	}
}

func TestJobHelpers(t *testing.T) {
	if id := newJobID("tf"); len(id) != 3+16 {
		t.Errorf("job id %q", id)
	}
}
