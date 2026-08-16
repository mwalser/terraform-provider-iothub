package twin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/common"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/identity"
	"github.com/mwalser/terraform-provider-iothub/internal/twinpatch"
)

var (
	_ datasource.DataSource              = &twinDataSource{}
	_ datasource.DataSourceWithConfigure = &twinDataSource{}
)

// NewDeviceDataSource returns the iothub_device_twin data source.
func NewDeviceDataSource() datasource.DataSource { return &twinDataSource{kind: deviceKind} }

// NewModuleDataSource returns the iothub_module_twin data source.
func NewModuleDataSource() datasource.DataSource { return &twinDataSource{kind: moduleKind} }

type twinDataSource struct {
	kind kind
	pd   *common.ProviderData
}

type dataSourceModel struct {
	ID                 types.String `tfsdk:"id"`
	DeviceID           types.String `tfsdk:"device_id"`
	ModuleID           types.String `tfsdk:"module_id"`
	Tags               types.String `tfsdk:"tags"`
	DesiredProperties  types.String `tfsdk:"desired_properties"`
	ReportedProperties types.String `tfsdk:"reported_properties"`
	DesiredVersion     types.Int64  `tfsdk:"desired_version"`
	ReportedVersion    types.Int64  `tfsdk:"reported_version"`
	ETag               types.String `tfsdk:"etag"`
	Version            types.Int64  `tfsdk:"version"`
	DeviceETag         types.String `tfsdk:"device_etag"`
	ModelID            types.String `tfsdk:"model_id"`
	Status             types.String `tfsdk:"status"`
	ConnectionState    types.String `tfsdk:"connection_state"`
	LastActivityTime   types.String `tfsdk:"last_activity_time"`
}

// deviceDataSourceModel is dataSourceModel without module_id.
type deviceDataSourceModel struct {
	ID                 types.String `tfsdk:"id"`
	DeviceID           types.String `tfsdk:"device_id"`
	Tags               types.String `tfsdk:"tags"`
	DesiredProperties  types.String `tfsdk:"desired_properties"`
	ReportedProperties types.String `tfsdk:"reported_properties"`
	DesiredVersion     types.Int64  `tfsdk:"desired_version"`
	ReportedVersion    types.Int64  `tfsdk:"reported_version"`
	ETag               types.String `tfsdk:"etag"`
	Version            types.Int64  `tfsdk:"version"`
	DeviceETag         types.String `tfsdk:"device_etag"`
	ModelID            types.String `tfsdk:"model_id"`
	Status             types.String `tfsdk:"status"`
	ConnectionState    types.String `tfsdk:"connection_state"`
	LastActivityTime   types.String `tfsdk:"last_activity_time"`
}

func (d *twinDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	if d.kind.isModule() {
		resp.TypeName = req.ProviderTypeName + "_module_twin"
	} else {
		resp.TypeName = req.ProviderTypeName + "_device_twin"
	}
}

func (d *twinDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	c := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{MarkdownDescription: desc, Computed: true}
	}
	subject, idFormat := "device", "The device ID"
	if d.kind.isModule() {
		subject, idFormat = "module", "`<device_id>/<module_id>`"
	}
	attrs := map[string]schema.Attribute{
		"id":                  c(idFormat + "."),
		"device_id":           schema.StringAttribute{MarkdownDescription: "Device ID.", Required: true},
		"tags":                c("All tags of the twin as a JSON string."),
		"desired_properties":  c("All desired properties as a JSON string, without `$metadata` and `$version`."),
		"reported_properties": c("All reported properties as a JSON string, without `$metadata` and `$version`."),
		"desired_version":     schema.Int64Attribute{MarkdownDescription: "`$version` of the desired properties.", Computed: true},
		"reported_version":    schema.Int64Attribute{MarkdownDescription: "`$version` of the reported properties.", Computed: true},
		"etag":                c("ETag of the twin."),
		"version":             schema.Int64Attribute{MarkdownDescription: "Version of the twin.", Computed: true},
		"device_etag":         c("ETag of the underlying identity."),
		"model_id":            c("IoT Plug and Play model ID announced by the " + subject + ", if any."),
		"status":              c("`enabled` or `disabled`, as set on the identity."),
		"connection_state":    c("`Connected` or `Disconnected`. Approximate."),
		"last_activity_time":  c("Last time the " + subject + " connected, sent or received a message."),
	}
	if d.kind.isModule() {
		attrs["module_id"] = schema.StringAttribute{MarkdownDescription: "Module ID.", Required: true}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "The complete " + subject + " twin: tags, desired properties and reported properties as JSON strings, " +
			"plus versions and the identity fields the twin carries.",
		Attributes: attrs,
	}
}

func (d *twinDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	pd, diags := common.ExpectProviderData(req.ProviderData)
	resp.Diagnostics.Append(diags...)
	d.pd = pd
}

func (d *twinDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data dataSourceModel
	if d.kind.isModule() {
		resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	} else {
		var dm deviceDataSourceModel
		resp.Diagnostics.Append(req.Config.Get(ctx, &dm)...)
		data = dataSourceModel{DeviceID: dm.DeviceID, ModuleID: types.StringNull()}
	}
	if resp.Diagnostics.HasError() {
		return
	}
	c, ok := common.DataSourceHub(d.pd, req, resp)
	if !ok {
		return
	}
	var tw *client.Twin
	var err error
	if d.kind.isModule() {
		tw, err = c.GetModuleTwin(ctx, data.DeviceID.ValueString(), data.ModuleID.ValueString())
	} else {
		tw, err = c.GetDeviceTwin(ctx, data.DeviceID.ValueString())
	}
	if err != nil {
		if client.IsNotFound(err) {
			what := fmt.Sprintf("device %q", data.DeviceID.ValueString())
			if d.kind.isModule() {
				what = fmt.Sprintf("module %q on device %q", data.ModuleID.ValueString(), data.DeviceID.ValueString())
			}
			resp.Diagnostics.AddError(d.kind.noun()+" not found", fmt.Sprintf("No %s exists in %s.", what, c.Hostname()))
			return
		}
		resp.Diagnostics.AddError("Cannot read IoT Hub "+d.kind.noun(), common.DescribeError(err))
		return
	}

	if d.kind.isModule() {
		data.ID = types.StringValue(common.ModuleID(tw.DeviceID, tw.ModuleID))
	} else {
		data.ID = types.StringValue(tw.DeviceID)
	}
	var dg diag.Diagnostics
	data.Tags, _, dg = sectionString(tw.Tags, "tags")
	resp.Diagnostics.Append(dg...)
	data.DesiredProperties, data.DesiredVersion, dg = sectionString(tw.Properties.Desired, "properties.desired")
	resp.Diagnostics.Append(dg...)
	data.ReportedProperties, data.ReportedVersion, dg = sectionString(tw.Properties.Reported, "properties.reported")
	resp.Diagnostics.Append(dg...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.ETag = types.StringValue(tw.ETag)
	data.Version = types.Int64Value(tw.Version)
	data.DeviceETag = identity.StringOrNull(tw.DeviceETag)
	data.ModelID = identity.StringOrNull(tw.ModelID)
	data.Status = identity.StringOrNull(tw.Status)
	data.ConnectionState = identity.StringOrNull(tw.ConnectionState)
	data.LastActivityTime = identity.TimeOrNull(tw.LastActivityTime)

	if d.kind.isModule() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &deviceDataSourceModel{
		ID: data.ID, DeviceID: data.DeviceID, Tags: data.Tags, DesiredProperties: data.DesiredProperties,
		ReportedProperties: data.ReportedProperties, DesiredVersion: data.DesiredVersion, ReportedVersion: data.ReportedVersion,
		ETag: data.ETag, Version: data.Version, DeviceETag: data.DeviceETag, ModelID: data.ModelID, Status: data.Status,
		ConnectionState: data.ConnectionState, LastActivityTime: data.LastActivityTime,
	})...)
}

// sectionString renders a twin section without its `$…` keys and returns
// its `$version` (null when absent, e.g. for tags).
func sectionString(raw json.RawMessage, what string) (types.String, types.Int64, diag.Diagnostics) {
	doc, diags := decodeSection(raw, what)
	if diags.HasError() {
		return types.StringNull(), types.Int64Null(), diags
	}
	version := types.Int64Null()
	if v, ok := doc["$version"].(json.Number); ok {
		if n, err := v.Int64(); err == nil {
			version = types.Int64Value(n)
		}
	}
	return types.StringValue(twinpatch.Encode(twinpatch.StripSystem(doc))), version, diags
}
