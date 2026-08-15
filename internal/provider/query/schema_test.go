package query

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
)

func TestSchema_ValidateImplementation(t *testing.T) {
	ctx := context.Background()
	var ds datasource.SchemaResponse
	NewDataSource().Schema(ctx, datasource.SchemaRequest{}, &ds)
	if diags := ds.Schema.ValidateImplementation(ctx); diags.HasError() {
		t.Fatalf("schema: %v", diags)
	}
	if _, ok := ds.Schema.Attributes["count"]; ok {
		t.Error("count is a reserved root attribute name")
	}
}
