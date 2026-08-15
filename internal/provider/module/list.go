package module

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/attr"
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

// NewListResource returns the iothub_module list resource (`terraform query`).
func NewListResource() list.ListResource { return &listResource{} }

type listResource struct {
	pd *common.ProviderData
}

type listModel struct {
	Hostname       types.String `tfsdk:"hostname"`
	DeviceID       types.String `tfsdk:"device_id"`
	QueryCondition types.String `tfsdk:"query_condition"`
}

func (l *listResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_module"
}

func (l *listResource) ListResourceConfigSchema(_ context.Context, _ list.ListResourceSchemaRequest, resp *list.ListResourceSchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists module identities for `terraform query` (`SELECT deviceId, moduleId FROM devices.modules WHERE …`, " +
			"confirmed with a registry read). The hub's system modules (`$edgeAgent`, `$edgeHub`) are skipped: they cannot be managed by " +
			"`iothub_module`.",
		Attributes: map[string]schema.Attribute{
			"hostname":  schema.StringAttribute{MarkdownDescription: common.HostnameAttributeDescription, Optional: true},
			"device_id": schema.StringAttribute{MarkdownDescription: "Only modules of this device.", Optional: true},
			"query_condition": schema.StringAttribute{
				MarkdownDescription: "Additional `WHERE` clause over `devices.modules` (e.g. `moduleId = 'telemetry'`, `tags.x = 1`).",
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
	hostname, ok, diags := common.ResolveHostname(cfg.Hostname, l.pd)
	if diags.HasError() {
		stream.Results = list.ListResultsStreamDiagnostics(diags)
		return
	}
	if !ok {
		diags.AddError("IoT Hub hostname unknown", "Set `hostname` in the list block or on the provider block.")
		stream.Results = list.ListResultsStreamDiagnostics(diags)
		return
	}
	c, diags := l.pd.ClientFor(ctx, hostname)
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
			result.DisplayName = fmt.Sprintf("%s/%s (%s)", mod.DeviceID, mod.ModuleID, c.Hostname())
			result.Diagnostics.Append(setIdentity(ctx, result.Identity, c.Hostname(), mod.DeviceID, mod.ModuleID)...)
			if req.IncludeResource {
				m := resourceModel{
					PrimaryKeyWO: types.StringNull(), SecondaryKeyWO: types.StringNull(),
					PrimaryKeyWOVersion: types.Int64Null(), SecondaryKeyWOVersion: types.Int64Null(),
					Timeouts: timeouts.Value{Object: types.ObjectNull(map[string]attr.Type{
						"create": types.StringType, "read": types.StringType, "update": types.StringType, "delete": types.StringType,
					})},
				}
				setState(&m, c.Hostname(), mod, m.writeOnlyKeys())
				result.Diagnostics.Append(result.Resource.Set(ctx, &m)...)
			}
			n++
			if !push(result) {
				return
			}
		}
	}
}
