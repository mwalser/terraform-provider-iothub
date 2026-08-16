package device

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/common"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/identity"
)

var (
	_ datasource.DataSource              = &dataSource{}
	_ datasource.DataSourceWithConfigure = &dataSource{}
)

// NewDataSource returns the iothub_device data source.
func NewDataSource() datasource.DataSource { return &dataSource{} }

type dataSource struct {
	pd *common.ProviderData
}

type dataSourceModel struct {
	ID                         types.String `tfsdk:"id"`
	DeviceID                   types.String `tfsdk:"device_id"`
	Status                     types.String `tfsdk:"status"`
	StatusReason               types.String `tfsdk:"status_reason"`
	EdgeEnabled                types.Bool   `tfsdk:"edge_enabled"`
	ParentScope                types.String `tfsdk:"parent_scope"`
	Authentication             types.Object `tfsdk:"authentication"`
	ETag                       types.String `tfsdk:"etag"`
	GenerationID               types.String `tfsdk:"generation_id"`
	DeviceScope                types.String `tfsdk:"device_scope"`
	ConnectionState            types.String `tfsdk:"connection_state"`
	ConnectionStateUpdatedTime types.String `tfsdk:"connection_state_updated_time"`
	LastActivityTime           types.String `tfsdk:"last_activity_time"`
	StatusUpdatedTime          types.String `tfsdk:"status_updated_time"`
	CloudToDeviceMessageCount  types.Int64  `tfsdk:"cloud_to_device_message_count"`
}

func (d *dataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_device"
}

func (d *dataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	c := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{MarkdownDescription: desc, Computed: true}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A device identity from the IoT Hub identity registry. Keys and connection strings are in the " +
			"`iothub_device_credentials` ephemeral resource.",
		Attributes: map[string]schema.Attribute{
			"id": c("The device ID."),
			"device_id": schema.StringAttribute{
				MarkdownDescription: "Device ID.",
				Required:            true,
			},
			"status":                        c("`enabled` or `disabled`."),
			"status_reason":                 c("Free-text reason for the status, if any."),
			"edge_enabled":                  schema.BoolAttribute{MarkdownDescription: "Whether the device is an IoT Edge device.", Computed: true},
			"parent_scope":                  c("Scope of the parent edge device, if the device is a child."),
			"authentication":                identity.AuthInfoAttribute("device"),
			"etag":                          c("ETag of the identity."),
			"generation_id":                 c("Hub-generated ID that changes when a device with the same `device_id` is re-created."),
			"device_scope":                  c("The device's own scope. Hub-generated for edge devices, the parent's scope for child leaf devices, otherwise empty."),
			"connection_state":              c("`Connected` or `Disconnected`. Approximate."),
			"connection_state_updated_time": c("When the connection state last changed."),
			"last_activity_time":            c("Last time the device connected, sent or received a message."),
			"status_updated_time":           c("When the status last changed."),
			"cloud_to_device_message_count": schema.Int64Attribute{MarkdownDescription: "Queued cloud-to-device messages.", Computed: true},
		},
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
	dev, err := c.GetDevice(ctx, data.DeviceID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.Diagnostics.AddError("Device not found", "No device with ID "+data.DeviceID.ValueString()+" exists in "+c.Hostname()+".")
			return
		}
		resp.Diagnostics.AddError("Cannot read IoT Hub device", common.DescribeError(err))
		return
	}

	data.ID = types.StringValue(dev.DeviceID)
	data.Status = types.StringValue(dev.Status)
	data.StatusReason = types.StringNull()
	if dev.StatusReason != nil && *dev.StatusReason != "" {
		data.StatusReason = types.StringValue(*dev.StatusReason)
	}
	data.EdgeEnabled = types.BoolValue(dev.Capabilities != nil && dev.Capabilities.IotEdge)
	data.ParentScope = identity.StringOrNull(parentScopeOf(dev))
	data.Authentication = identity.AuthInfoFromHub(dev.Authentication)
	data.ETag = types.StringValue(dev.ETag)
	data.GenerationID = types.StringValue(dev.GenerationID)
	data.DeviceScope = identity.StringOrNull(dev.DeviceScope)
	data.ConnectionState = identity.StringOrNull(dev.ConnectionState)
	data.ConnectionStateUpdatedTime = identity.TimeOrNull(dev.ConnectionStateUpdatedTime)
	data.LastActivityTime = identity.TimeOrNull(dev.LastActivityTime)
	data.StatusUpdatedTime = identity.TimeOrNull(dev.StatusUpdatedTime)
	data.CloudToDeviceMessageCount = types.Int64Value(dev.CloudToDeviceMessageCount)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
