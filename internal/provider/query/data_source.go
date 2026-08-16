// Package query implements the iothub_query data source (POST /devices/query).
package query

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/mwalser/terraform-provider-iothub/internal/provider/common"
)

var (
	_ datasource.DataSource              = &dataSource{}
	_ datasource.DataSourceWithConfigure = &dataSource{}
)

// NewDataSource returns the iothub_query data source.
func NewDataSource() datasource.DataSource { return &dataSource{} }

type dataSource struct {
	pd *common.ProviderData
}

type model struct {
	ID       types.String `tfsdk:"id"`
	Query    types.String `tfsdk:"query"`
	Results  types.List   `tfsdk:"results"`
	ItemType types.String `tfsdk:"item_type"`
}

func (d *dataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_query"
}

func (d *dataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Runs an [IoT Hub query language](https://learn.microsoft.com/azure/iot-hub/iot-hub-devguide-query-language) " +
			"statement against the hub and returns all results.\n\n" +
			"Results are JSON strings, so use `jsondecode()` on them. `SELECT * FROM devices` yields whole twins, a projection " +
			"yields the selected columns, and `FROM devices.jobs` yields job records. The query index is eventually consistent: " +
			"a new device is visible a few seconds after creation, and a deleted device can be listed for a while afterwards. " +
			"For a single known device, prefer the `iothub_device` or `iothub_device_twin` data source.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{MarkdownDescription: "A short hash of the query.", Computed: true},
			"query": schema.StringAttribute{
				MarkdownDescription: "The statement, for example `SELECT deviceId, tags.site FROM devices WHERE tags.fleet.region = 'eu'`.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"results": schema.ListAttribute{
				MarkdownDescription: "One JSON string per result row, in the order the hub returns them.",
				ElementType:         types.StringType,
				Computed:            true,
			},
			"item_type": schema.StringAttribute{MarkdownDescription: "The kind of result rows as reported by the hub: `Raw` for a projection, `Twin` for `SELECT * FROM devices` or `devices.modules`, `DeviceJob` for `FROM devices.jobs`.", Computed: true},
		},
	}
}

func (d *dataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	pd, diags := common.ExpectProviderData(req.ProviderData)
	resp.Diagnostics.Append(diags...)
	d.pd = pd
}

func (d *dataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data model
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, ok := common.DataSourceHub(d.pd, req, resp)
	if !ok {
		return
	}
	query := data.Query.ValueString()
	items, itemType, err := c.Query(ctx, query)
	if err != nil {
		resp.Diagnostics.AddError("IoT Hub query failed", "Query: "+query+"\n\n"+common.DescribeError(err))
		return
	}
	tflog.Debug(ctx, "query done", map[string]any{"count": len(items), "item_type": itemType})

	elems := make([]attr.Value, 0, len(items))
	for _, it := range items {
		elems = append(elems, types.StringValue(string(it)))
	}
	list, diags := types.ListValue(types.StringType, elems)
	resp.Diagnostics.Append(diags...)
	sum := sha256.Sum256([]byte(query))
	data.ID = types.StringValue(hex.EncodeToString(sum[:8]))
	data.Results = list
	data.ItemType = types.StringValue(itemType)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
