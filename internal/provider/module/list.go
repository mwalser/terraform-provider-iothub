package module

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/list"
	"github.com/hashicorp/terraform-plugin-framework/list/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/common"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/identity"
)

var (
	_ list.ListResource              = &listResource{}
	_ list.ListResourceWithConfigure = &listResource{}
)

// NewListResource returns the iothub_module list resource (`terraform query`).
func NewListResource() list.ListResource { return &listResource{} }

type listResource struct {
	pd *common.ProviderData
}

type listModel struct {
	DeviceID       types.String `tfsdk:"device_id"`
	QueryCondition types.String `tfsdk:"query_condition"`
}

func (l *listResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_module"
}

func (l *listResource) ListResourceConfigSchema(_ context.Context, _ list.ListResourceSchemaRequest, resp *list.ListResourceSchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists module identities for `terraform query`, for example to generate `import` blocks for an existing fleet. " +
			"The hub's system modules (`$edgeAgent`, `$edgeHub`) are skipped because `iothub_module` cannot manage them. Results carry no keys.",
		Attributes: map[string]schema.Attribute{
			"device_id": schema.StringAttribute{MarkdownDescription: "Only modules of this device.", Optional: true},
			"query_condition": schema.StringAttribute{
				MarkdownDescription: "`WHERE` clause over `devices.modules`, for example `moduleId = 'telemetry'` or `tags.x = 1`. Combined with `device_id` when both are set.",
				Optional:            true,
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
	var where []string
	if id := cfg.DeviceID.ValueString(); id != "" {
		where = append(where, fmt.Sprintf("deviceId = '%s'", strings.ReplaceAll(id, "'", "''")))
	}
	if cond := strings.TrimSpace(cfg.QueryCondition.ValueString()); cond != "" {
		where = append(where, "("+cond+")")
	}
	query := "SELECT deviceId, moduleId FROM devices.modules"
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	items, _, err := c.Query(ctx, query)
	if err != nil {
		diags.AddError("IoT Hub query failed", "Query: "+query+"\n\n"+common.DescribeError(err))
		stream.Results = list.ListResultsStreamDiagnostics(diags)
		return
	}
	type pair struct{ device, module string }
	var pairs []pair
	for _, it := range items {
		var row struct {
			DeviceID string `json:"deviceId"`
			ModuleID string `json:"moduleId"`
		}
		if json.Unmarshal(it, &row) == nil && row.DeviceID != "" && row.ModuleID != "" && !strings.HasPrefix(row.ModuleID, "$") {
			pairs = append(pairs, pair{row.DeviceID, row.ModuleID})
		}
	}
	tflog.Debug(ctx, "listing modules", map[string]any{"query": query, "candidates": len(pairs)})

	stream.Results = func(push func(list.ListResult) bool) {
		var n int64
		for _, p := range pairs {
			if req.Limit > 0 && n >= req.Limit {
				return
			}
			mod, err := c.GetModule(ctx, p.device, p.module)
			if err != nil {
				if client.IsNotFound(err) {
					continue // query-index ghost
				}
				var d diag.Diagnostics
				d.AddError(fmt.Sprintf("Cannot read IoT Hub module %s/%s", p.device, p.module), common.DescribeError(err))
				push(list.ListResult{Diagnostics: d})
				return
			}
			result := req.NewListResult(ctx)
			result.DisplayName = common.ModuleID(mod.DeviceID, mod.ModuleID)
			result.Diagnostics.Append(setIdentity(ctx, result.Identity, mod.DeviceID, mod.ModuleID)...)
			if req.IncludeResource {
				// No key material in list results (they are printed and
				// generated into config); the first refresh after an import
				// fills the keys in as usual.
				m := resourceModel{
					PrimaryKeyWO: types.StringNull(), SecondaryKeyWO: types.StringNull(),
					PrimaryKeyWOVersion: types.Int64Null(), SecondaryKeyWOVersion: types.Int64Null(),
				}
				setState(&m, mod, m.writeOnlyKeys())
				m.Authentication = withoutKeys(ctx, m.Authentication)
				result.Diagnostics.Append(result.Resource.Set(ctx, &m)...)
			}
			n++
			if !push(result) {
				return
			}
		}
	}
}

// withoutKeys returns the authentication object with both symmetric keys
// null.
func withoutKeys(ctx context.Context, o types.Object) types.Object {
	a, ok, _ := identity.AuthFromObject(ctx, o)
	if !ok {
		return o
	}
	a.PrimaryKey, a.SecondaryKey = types.StringNull(), types.StringNull()
	return a.Object()
}
