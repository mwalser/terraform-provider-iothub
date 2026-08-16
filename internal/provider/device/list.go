package device

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/list"
	"github.com/hashicorp/terraform-plugin-framework/list/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/common"
)

var (
	_ list.ListResource              = &listResource{}
	_ list.ListResourceWithConfigure = &listResource{}
)

// NewListResource returns the iothub_device list resource (`terraform query`).
func NewListResource() list.ListResource { return &listResource{} }

type listResource struct {
	pd *common.ProviderData
}

type listModel struct {
	QueryCondition types.String `tfsdk:"query_condition"`
}

func (l *listResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_device"
}

func (l *listResource) ListResourceConfigSchema(_ context.Context, _ list.ListResourceSchemaRequest, resp *list.ListResourceSchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists device identities for `terraform query`, for example to generate `import` blocks for an existing fleet. Results carry no keys.",
		Attributes: map[string]schema.Attribute{
			"query_condition": schema.StringAttribute{
				MarkdownDescription: "`WHERE` clause over `deviceId`, `tags`, `properties` and `capabilities`, for example `tags.site = 'munich'` or " +
					"`capabilities.iotEdge = true`. All devices when omitted.",
				Optional: true,
			},
		},
	}
}

func (l *listResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	pd, diags := common.ExpectProviderData(req.ProviderData)
	resp.Diagnostics.Append(diags...)
	l.pd = pd
}

func (l *listResource) List(ctx context.Context, req list.ListRequest, stream *list.ListResultsStream) {
	var cfg listModel
	if diags := req.Config.Get(ctx, &cfg); diags.HasError() {
		stream.Results = list.ListResultsStreamDiagnostics(diags)
		return
	}
	c, diags := l.pd.HubOrError()
	if diags.HasError() {
		stream.Results = list.ListResultsStreamDiagnostics(diags)
		return
	}
	query := "SELECT deviceId FROM devices"
	if cond := strings.TrimSpace(cfg.QueryCondition.ValueString()); cond != "" {
		query += " WHERE " + cond
	}
	ids, diags := common.QueryIDs(ctx, c, query, "deviceId")
	if diags.HasError() {
		stream.Results = list.ListResultsStreamDiagnostics(diags)
		return
	}
	tflog.Debug(ctx, "listing devices", map[string]any{"query": query, "candidates": len(ids)})

	stream.Results = func(push func(list.ListResult) bool) {
		var n int64
		for _, id := range ids {
			if req.Limit > 0 && n >= req.Limit {
				return
			}
			dev, err := c.GetDevice(ctx, id)
			if err != nil {
				if client.IsNotFound(err) {
					continue // query-index ghost (deleted device still indexed)
				}
				var d diag.Diagnostics
				d.AddError("Cannot read IoT Hub device "+id, common.DescribeError(err))
				push(list.ListResult{Diagnostics: d})
				return
			}
			result := req.NewListResult(ctx)
			result.DisplayName = dev.DeviceID
			result.Diagnostics.Append(setIdentity(ctx, result.Identity, dev.DeviceID)...)
			if req.IncludeResource {
				// No key material in list results (they are printed and
				// generated into config); the first refresh after an import
				// fills the keys in as usual.
				m := resourceModel{}
				m.PrimaryKeyWO, m.SecondaryKeyWO = types.StringNull(), types.StringNull()
				m.PrimaryKeyWOVersion, m.SecondaryKeyWOVersion = types.Int64Null(), types.Int64Null()
				setState(&m, dev, false, false)
				result.Diagnostics.Append(result.Resource.Set(ctx, &m)...)
			}
			n++
			if !push(result) {
				return
			}
		}
	}
}
