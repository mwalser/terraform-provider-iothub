package module

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/common"
)

var (
	_ datasource.DataSource              = &modulesDataSource{}
	_ datasource.DataSourceWithConfigure = &modulesDataSource{}
)

// NewModulesDataSource returns the iothub_modules data source.
func NewModulesDataSource() datasource.DataSource { return &modulesDataSource{} }

type modulesDataSource struct {
	pd *common.ProviderData
}

type modulesModel struct {
	ID       types.String `tfsdk:"id"`
	Hostname types.String `tfsdk:"hostname"`
	DeviceID types.String `tfsdk:"device_id"`
	Modules  types.List   `tfsdk:"modules"`
}

func (d *modulesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_modules"
}

func (d *modulesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "All module identities of a device, including the hub-managed `$edgeAgent` and `$edgeHub` on IoT " +
			"Edge devices. Symmetric keys are not exposed. Use `iothub_module_credentials` for those.",
		Attributes: map[string]schema.Attribute{
			"id":        schema.StringAttribute{MarkdownDescription: "`<hostname>/devices/<device_id>/modules`.", Computed: true},
			"hostname":  schema.StringAttribute{MarkdownDescription: common.HostnameAttributeDescription, Optional: true, Computed: true, Validators: common.HostnameValidators()},
			"device_id": schema.StringAttribute{MarkdownDescription: "Device ID.", Required: true},
			"modules": schema.ListNestedAttribute{
				MarkdownDescription: "The device's modules, in the order the hub returns them.",
				Computed:            true,
				NestedObject:        schema.NestedAttributeObject{Attributes: infoAttributes()},
			},
		},
	}
}

func (d *modulesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	pd, diags := common.ExpectProviderData(req.ProviderData)
	resp.Diagnostics.Append(diags...)
	d.pd = pd
}

func (d *modulesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data modulesModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	hostname, ok, diags := common.ResolveHostname(data.Hostname, d.pd)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !ok {
		if req.ClientCapabilities.DeferralAllowed {
			resp.Deferred = &datasource.Deferred{Reason: datasource.DeferredReasonProviderConfigUnknown}
		}
		return
	}
	c, diags := d.pd.ClientFor(ctx, hostname)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	mods, err := c.ListModules(ctx, data.DeviceID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.Diagnostics.AddError("Device not found", fmt.Sprintf("No device with ID %q exists in %s.", data.DeviceID.ValueString(), c.Hostname()))
			return
		}
		resp.Diagnostics.AddError("Cannot list IoT Hub modules", common.DescribeError(err))
		return
	}
	elems := make([]attr.Value, 0, len(mods))
	for i := range mods {
		elems = append(elems, infoFromHub(&mods[i]).object())
	}
	list, diags := types.ListValue(types.ObjectType{AttrTypes: infoAttrTypes}, elems)
	resp.Diagnostics.Append(diags...)
	data.ID = types.StringValue(common.ResourceID(c.Hostname(), "devices", data.DeviceID.ValueString(), "modules"))
	data.Hostname = types.StringValue(c.Hostname())
	data.Modules = list
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
