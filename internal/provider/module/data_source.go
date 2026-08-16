package module

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/common"
)

var (
	_ datasource.DataSource              = &dataSource{}
	_ datasource.DataSourceWithConfigure = &dataSource{}
)

// NewDataSource returns the iothub_module data source.
func NewDataSource() datasource.DataSource { return &dataSource{} }

type dataSource struct {
	pd *common.ProviderData
}

type dataSourceModel struct {
	ID                         types.String `tfsdk:"id"`
	DeviceID                   types.String `tfsdk:"device_id"`
	ModuleID                   types.String `tfsdk:"module_id"`
	ManagedBy                  types.String `tfsdk:"managed_by"`
	AuthenticationType         types.String `tfsdk:"authentication_type"`
	PrimaryThumbprint          types.String `tfsdk:"primary_thumbprint"`
	SecondaryThumbprint        types.String `tfsdk:"secondary_thumbprint"`
	ETag                       types.String `tfsdk:"etag"`
	GenerationID               types.String `tfsdk:"generation_id"`
	ConnectionState            types.String `tfsdk:"connection_state"`
	ConnectionStateUpdatedTime types.String `tfsdk:"connection_state_updated_time"`
	LastActivityTime           types.String `tfsdk:"last_activity_time"`
	CloudToDeviceMessageCount  types.Int64  `tfsdk:"cloud_to_device_message_count"`
}

func (d *dataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_module"
}

func (d *dataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := infoAttributes()
	attrs["id"] = schema.StringAttribute{MarkdownDescription: "`<device_id>/<module_id>`.", Computed: true}
	attrs["device_id"] = schema.StringAttribute{MarkdownDescription: "Device ID.", Required: true}
	attrs["module_id"] = schema.StringAttribute{MarkdownDescription: "Module ID. System modules such as `$edgeAgent` can be read too.", Required: true}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A module identity from the IoT Hub identity registry. Symmetric keys are not exposed here. " +
			"Use the `iothub_module_credentials` ephemeral resource for keys and connection strings.",
		Attributes: attrs,
	}
}

func (d *dataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	pd, diags := common.ExpectProviderData(req.ProviderData)
	resp.Diagnostics.Append(diags...)
	d.pd = pd
}

func (d *dataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data dataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, ok := common.DataSourceHub(d.pd, req, resp)
	if !ok {
		return
	}
	mod, err := c.GetModule(ctx, data.DeviceID.ValueString(), data.ModuleID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.Diagnostics.AddError("Module not found",
				fmt.Sprintf("No module with ID %q exists on device %q in %s (or the device does not exist).", data.ModuleID.ValueString(), data.DeviceID.ValueString(), c.Hostname()))
			return
		}
		resp.Diagnostics.AddError("Cannot read IoT Hub module", common.DescribeError(err))
		return
	}
	info := infoFromHub(mod)
	data.ID = types.StringValue(common.ModuleID(mod.DeviceID, mod.ModuleID))
	data.DeviceID = types.StringValue(mod.DeviceID)
	data.ModuleID = info.ModuleID
	data.ManagedBy = info.ManagedBy
	data.AuthenticationType = info.AuthenticationType
	data.PrimaryThumbprint = info.PrimaryThumbprint
	data.SecondaryThumbprint = info.SecondaryThumbprint
	data.ETag = info.ETag
	data.GenerationID = info.GenerationID
	data.ConnectionState = info.ConnectionState
	data.ConnectionStateUpdatedTime = info.ConnectionStateUpdatedTime
	data.LastActivityTime = info.LastActivityTime
	data.CloudToDeviceMessageCount = info.CloudToDeviceMessageCount
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
