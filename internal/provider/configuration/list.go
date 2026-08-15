package configuration

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/list"
	"github.com/hashicorp/terraform-plugin-framework/list/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/mwalser/terraform-provider-iothub/internal/provider/common"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/jsondoc"
)

var (
	_ list.ListResource              = &listResource{}
	_ list.ListResourceWithConfigure = &listResource{}
)

// NewConfigurationListResource returns the iothub_configuration list resource.
func NewConfigurationListResource() list.ListResource { return &listResource{kind: configurationKind} }

// NewEdgeDeploymentListResource returns the iothub_edge_deployment list resource.
func NewEdgeDeploymentListResource() list.ListResource {
	return &listResource{kind: edgeDeploymentKind}
}

type listResource struct {
	kind kind
	pd   *common.ProviderData
}

type listModel struct {
	Hostname types.String `tfsdk:"hostname"`
}

func (l *listResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + l.kind.typeSuffix()
}

func (l *listResource) ListResourceConfigSchema(_ context.Context, _ list.ListResourceSchemaRequest, resp *list.ListResourceSchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists every " + l.kind.noun() + " of the hub for `terraform query` (`GET /configurations`, filtered by content kind).",
		Attributes: map[string]schema.Attribute{
			"hostname": schema.StringAttribute{MarkdownDescription: common.HostnameAttributeDescription, Optional: true},
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
	all, err := c.ListConfigurations(ctx)
	if err != nil {
		diags.AddError("Cannot list IoT Hub configurations", common.DescribeError(err))
		stream.Results = list.ListResultsStreamDiagnostics(diags)
		return
	}
	tflog.Debug(ctx, "listing "+l.kind.noun()+"s", map[string]any{"total": len(all)})

	stream.Results = func(push func(list.ListResult) bool) {
		var n int64
		for i := range all {
			cfg := &all[i]
			if edge, _ := contentKind(cfg); edge != l.kind.isEdge() {
				continue
			}
			if req.Limit > 0 && n >= req.Limit {
				return
			}
			result := req.NewListResult(ctx)
			result.DisplayName = fmt.Sprintf("%s (%s)", cfg.ID, c.Hostname())
			result.Diagnostics.Append(l.kind.setIdentity(ctx, result.Identity, c.Hostname(), cfg.ID)...)
			if req.IncludeResource {
				m := model{
					Labels: types.MapNull(types.StringType), Metrics: types.MapNull(types.StringType),
					DeviceContent: jsondoc.NewNull(ContentType), ModuleContent: jsondoc.NewNull(ContentType), ModulesContent: jsondoc.NewNull(ModulesContentType),
					Timeouts: timeouts.Value{Object: types.ObjectNull(map[string]attr.Type{
						"create": types.StringType, "read": types.StringType, "update": types.StringType, "delete": types.StringType,
					})},
				}
				l.kind.fromHub(&m, c.Hostname(), cfg, m)
				result.Diagnostics.Append(l.kind.set(ctx, result.Resource, m)...)
			}
			n++
			if !push(result) {
				return
			}
		}
	}
}
