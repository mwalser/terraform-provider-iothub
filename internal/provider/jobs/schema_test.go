package jobs

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
)

func TestSchemas_ValidateImplementation(t *testing.T) {
	ctx := context.Background()
	for name, d := range map[string]datasource.DataSource{"scheduled": NewScheduledJobDataSource(), "import_export": NewImportExportJobDataSource()} {
		var resp datasource.SchemaResponse
		d.Schema(ctx, datasource.SchemaRequest{}, &resp)
		if diags := resp.Schema.ValidateImplementation(ctx); diags.HasError() {
			t.Fatalf("%s: %v", name, diags)
		}
	}
}
