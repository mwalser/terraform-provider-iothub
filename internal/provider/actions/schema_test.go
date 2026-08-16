package actions

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
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

func TestDurationValidator(t *testing.T) {
	ctx := context.Background()
	v := durationValidator{}
	for _, value := range []string{"30m", "2h", "1.5s"} {
		var resp validator.StringResponse
		v.ValidateString(ctx, validator.StringRequest{Path: path.Root("timeout"), ConfigValue: types.StringValue(value)}, &resp)
		if resp.Diagnostics.HasError() {
			t.Errorf("%q rejected: %v", value, resp.Diagnostics)
		}
	}
	for _, value := range []string{"zero", "0s", "-1m"} {
		var resp validator.StringResponse
		v.ValidateString(ctx, validator.StringRequest{Path: path.Root("timeout"), ConfigValue: types.StringValue(value)}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Errorf("%q accepted", value)
		}
	}
}

func TestValidateMethodTimeouts(t *testing.T) {
	p := path.Root("method").AtName("connect_timeout_seconds")
	if diags := validateMethodTimeouts(types.Int64Value(30), types.Int64Value(31), p); !diags.HasError() {
		t.Fatal("connect timeout greater than response timeout accepted")
	}
	if diags := validateMethodTimeouts(types.Int64Value(30), types.Int64Value(30), p); diags.HasError() {
		t.Fatalf("equal timeouts rejected: %v", diags)
	}
	if diags := validateMethodTimeouts(types.Int64Null(), types.Int64Value(31), p); !diags.HasError() {
		t.Fatal("connect timeout greater than the default response timeout accepted")
	}
}
