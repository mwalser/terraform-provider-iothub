// Package statistics implements the iothub_statistics data source.
package statistics

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/mwalser/terraform-provider-iothub/internal/provider/common"
)

var (
	_ datasource.DataSource              = &dataSource{}
	_ datasource.DataSourceWithConfigure = &dataSource{}
)

// NewDataSource returns the iothub_statistics data source.
func NewDataSource() datasource.DataSource { return &dataSource{} }

type dataSource struct {
	pd *common.ProviderData
}

type model struct {
	TotalDeviceCount     types.Int64 `tfsdk:"total_device_count"`
	EnabledDeviceCount   types.Int64 `tfsdk:"enabled_device_count"`
	DisabledDeviceCount  types.Int64 `tfsdk:"disabled_device_count"`
	ConnectedDeviceCount types.Int64 `tfsdk:"connected_device_count"`
}

func (d *dataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_statistics"
}

func (d *dataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Identity registry and service statistics of an IoT Hub. The service updates the counts " +
			"asynchronously, so they can lag behind registry changes and device connections by minutes. Treat them as approximate.",
		Attributes: map[string]schema.Attribute{
			"total_device_count": schema.Int64Attribute{
				MarkdownDescription: "Number of device identities in the registry.",
				Computed:            true,
			},
			"enabled_device_count": schema.Int64Attribute{
				MarkdownDescription: "Number of enabled device identities.",
				Computed:            true,
			},
			"disabled_device_count": schema.Int64Attribute{
				MarkdownDescription: "Number of disabled device identities.",
				Computed:            true,
			},
			"connected_device_count": schema.Int64Attribute{
				MarkdownDescription: "Number of currently connected devices (approximate).",
				Computed:            true,
			},
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

	tflog.Debug(ctx, "reading IoT Hub statistics", map[string]any{"hostname": c.Hostname()})
	reg, err := c.GetRegistryStatistics(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Cannot read IoT Hub registry statistics", common.DescribeError(err))
		return
	}
	svc, err := c.GetServiceStatistics(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Cannot read IoT Hub service statistics", common.DescribeError(err))
		return
	}

	data.TotalDeviceCount = types.Int64Value(reg.TotalDeviceCount)
	data.EnabledDeviceCount = types.Int64Value(reg.EnabledDeviceCount)
	data.DisabledDeviceCount = types.Int64Value(reg.DisabledDeviceCount)
	data.ConnectedDeviceCount = types.Int64Value(svc.ConnectedDeviceCount)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
