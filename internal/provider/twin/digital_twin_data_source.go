package twin

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/common"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/identity"
)

var (
	_ datasource.DataSource              = &digitalTwinDataSource{}
	_ datasource.DataSourceWithConfigure = &digitalTwinDataSource{}
)

// NewDigitalTwinDataSource returns the iothub_digital_twin data source.
func NewDigitalTwinDataSource() datasource.DataSource { return &digitalTwinDataSource{} }

type digitalTwinDataSource struct {
	pd *common.ProviderData
}

type digitalTwinModel struct {
	ID       types.String `tfsdk:"id"`
	DeviceID types.String `tfsdk:"device_id"`
	Document types.String `tfsdk:"document"`
	ModelID  types.String `tfsdk:"model_id"`
	ETag     types.String `tfsdk:"etag"`
}

func (d *digitalTwinDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_digital_twin"
}

func (d *digitalTwinDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The IoT Plug and Play digital twin of a device. The hub derives this document from the device twin and " +
			"the device's DTDL model. It contains `$dtId`, `$metadata.$model`, root-level properties and components. Components are " +
			"objects with their own `$metadata`.\n\n" +
			"The digital twin is read-only here. Writable Plug and Play properties are twin desired properties, so manage them with " +
			"`iothub_device_twin`. In `iothub_device_twin`, component properties need the `\"__t\": \"c\"` marker. A device that " +
			"never announced a model has a null `model_id` and a document without properties.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{MarkdownDescription: "The device ID.", Computed: true},
			"device_id": schema.StringAttribute{
				MarkdownDescription: "ID of the device.",
				Required:            true,
				Validators:          []validator.String{identity.IDValidator()},
			},
			"document": schema.StringAttribute{MarkdownDescription: "The digital twin document as a JSON string, verbatim from the hub. Use `jsondecode()`.", Computed: true},
			"model_id": schema.StringAttribute{MarkdownDescription: "The DTDL model ID announced by the device. Null when the device is not a Plug and Play device.", Computed: true},
			"etag":     schema.StringAttribute{MarkdownDescription: "ETag of the digital twin.", Computed: true},
		},
	}
}

func (d *digitalTwinDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	pd, diags := common.ExpectProviderData(req.ProviderData)
	resp.Diagnostics.Append(diags...)
	d.pd = pd
}

func (d *digitalTwinDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data digitalTwinModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, ok := common.DataSourceHub(d.pd, req, resp)
	if !ok {
		return
	}
	dt, err := c.GetDigitalTwin(ctx, data.DeviceID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.Diagnostics.AddError("Digital twin not found", fmt.Sprintf("No device %q exists in %s.", data.DeviceID.ValueString(), c.Hostname()))
			return
		}
		resp.Diagnostics.AddError("Cannot read IoT Hub digital twin", common.DescribeError(err))
		return
	}
	data.ID = data.DeviceID
	data.Document = types.StringValue(string(dt.Document))
	data.ModelID = identity.StringOrNull(dt.ModelID)
	data.ETag = identity.StringOrNull(dt.ETag)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
