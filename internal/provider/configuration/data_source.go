package configuration

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/common"
	"github.com/mwalser/terraform-provider-iothub/internal/twinpatch"
)

var (
	_ datasource.DataSource              = &configDataSource{}
	_ datasource.DataSourceWithConfigure = &configDataSource{}
)

// NewConfigurationDataSource returns the iothub_configuration data source.
func NewConfigurationDataSource() datasource.DataSource {
	return &configDataSource{kind: configurationKind}
}

// NewEdgeDeploymentDataSource returns the iothub_edge_deployment data source.
func NewEdgeDeploymentDataSource() datasource.DataSource {
	return &configDataSource{kind: edgeDeploymentKind}
}

type configDataSource struct {
	kind kind
	pd   *common.ProviderData
}

type dsModel struct {
	ID                 types.String `tfsdk:"id"`
	Hostname           types.String `tfsdk:"hostname"`
	ConfigurationID    types.String `tfsdk:"configuration_id"`
	TargetCondition    types.String `tfsdk:"target_condition"`
	Priority           types.Int64  `tfsdk:"priority"`
	Labels             types.Map    `tfsdk:"labels"`
	DeviceContent      types.String `tfsdk:"device_content"`
	ModuleContent      types.String `tfsdk:"module_content"`
	Metrics            types.Map    `tfsdk:"metrics"`
	SchemaVersion      types.String `tfsdk:"schema_version"`
	ETag               types.String `tfsdk:"etag"`
	CreatedTimeUTC     types.String `tfsdk:"created_time_utc"`
	LastUpdatedTimeUTC types.String `tfsdk:"last_updated_time_utc"`
	SystemMetrics      types.Map    `tfsdk:"system_metrics"`
	MetricResults      types.Map    `tfsdk:"metric_results"`
}

type edgeDSModel struct {
	ID                 types.String `tfsdk:"id"`
	Hostname           types.String `tfsdk:"hostname"`
	DeploymentID       types.String `tfsdk:"deployment_id"`
	TargetCondition    types.String `tfsdk:"target_condition"`
	Priority           types.Int64  `tfsdk:"priority"`
	Labels             types.Map    `tfsdk:"labels"`
	ModulesContent     types.String `tfsdk:"modules_content"`
	Metrics            types.Map    `tfsdk:"metrics"`
	SchemaVersion      types.String `tfsdk:"schema_version"`
	ETag               types.String `tfsdk:"etag"`
	CreatedTimeUTC     types.String `tfsdk:"created_time_utc"`
	LastUpdatedTimeUTC types.String `tfsdk:"last_updated_time_utc"`
	SystemMetrics      types.Map    `tfsdk:"system_metrics"`
	MetricResults      types.Map    `tfsdk:"metric_results"`
}

func (d *configDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + d.kind.typeSuffix()
}

func (d *configDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	c := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{MarkdownDescription: desc, Computed: true}
	}
	attrs := map[string]schema.Attribute{
		"id":                    c("`<hostname>/configurations/<" + d.kind.idAttr() + ">`."),
		"hostname":              schema.StringAttribute{MarkdownDescription: common.HostnameAttributeDescription, Optional: true, Computed: true, Validators: common.HostnameValidators()},
		d.kind.idAttr():         schema.StringAttribute{MarkdownDescription: "ID of the " + d.kind.noun() + ".", Required: true},
		"target_condition":      c(targetConditionSummary(d.kind)),
		"priority":              schema.Int64Attribute{MarkdownDescription: "Priority. Higher wins when several " + d.kind.noun() + "s target the same device.", Computed: true},
		"labels":                schema.MapAttribute{MarkdownDescription: "Free-form labels.", ElementType: types.StringType, Computed: true},
		"metrics":               schema.MapAttribute{MarkdownDescription: "Custom metrics: a map from metric name to an IoT Hub query.", ElementType: types.StringType, Computed: true},
		"schema_version":        c("Version string of the " + d.kind.noun() + " document, if set."),
		"etag":                  c("ETag of the " + d.kind.noun() + "."),
		"created_time_utc":      c("Creation time."),
		"last_updated_time_utc": c("Last update time."),
		"system_metrics":        schema.MapAttribute{MarkdownDescription: systemMetricsDescription(d.kind), ElementType: types.Int64Type, Computed: true},
		"metric_results":        schema.MapAttribute{MarkdownDescription: "Latest results of the custom `metrics`, by name.", ElementType: types.Int64Type, Computed: true},
	}
	if d.kind.isEdge() {
		attrs["modules_content"] = c("The deployment's `modulesContent` as a JSON string.")
	} else {
		attrs["device_content"] = c("Device twin content as a JSON string. Null for module configurations.")
		attrs["module_content"] = c("Module twin content as a JSON string. Null for device configurations.")
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "An existing " + d.kind.noun() + " with its content, target, labels, metric queries and the hub's latest metric results.",
		Attributes:          attrs,
	}
}

func (d *configDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	pd, diags := common.ExpectProviderData(req.ProviderData)
	resp.Diagnostics.Append(diags...)
	d.pd = pd
}

func (d *configDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var hostnameCfg, id types.String
	if d.kind.isEdge() {
		var m edgeDSModel
		resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
		hostnameCfg, id = m.Hostname, m.DeploymentID
	} else {
		var m dsModel
		resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
		hostnameCfg, id = m.Hostname, m.ConfigurationID
	}
	if resp.Diagnostics.HasError() {
		return
	}
	hostname, ok, diags := common.ResolveHostname(hostnameCfg, d.pd)
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
	cfg, err := c.GetConfiguration(ctx, id.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.Diagnostics.AddError(d.kind.noun()+" not found", fmt.Sprintf("No configuration or deployment with ID %q exists in %s.", id.ValueString(), c.Hostname()))
			return
		}
		resp.Diagnostics.AddError("Cannot read IoT Hub "+d.kind.noun(), common.DescribeError(err))
		return
	}
	if edge, section := contentKind(cfg); edge != d.kind.isEdge() {
		other := configurationKind
		if edge {
			other = edgeDeploymentKind
		}
		resp.Diagnostics.AddError("Wrong data source for this "+d.kind.noun(),
			fmt.Sprintf("%q carries %s and is an %s; read it with data.%s instead.", cfg.ID, section, other.noun(), other.resourceType()))
		return
	}

	labels := stringMap(cfg.Labels, types.MapValueMust(types.StringType, nil))
	var queries map[string]string
	var sys, res map[string]int64
	if cfg.Metrics != nil {
		queries, res = cfg.Metrics.Queries, cfg.Metrics.Results
	}
	if cfg.SystemMetrics != nil {
		sys = cfg.SystemMetrics.Results
	}
	base := dsModel{
		ID:                 types.StringValue(resourceID(c.Hostname(), cfg.ID)),
		Hostname:           types.StringValue(c.Hostname()),
		ConfigurationID:    types.StringValue(cfg.ID),
		TargetCondition:    types.StringValue(cfg.TargetCondition),
		Priority:           types.Int64Value(cfg.Priority),
		Labels:             labels,
		Metrics:            stringMap(queries, types.MapValueMust(types.StringType, nil)),
		SchemaVersion:      types.StringNull(),
		ETag:               types.StringValue(cfg.ETag),
		CreatedTimeUTC:     types.StringValue(cfg.CreatedTimeUTC),
		LastUpdatedTimeUTC: types.StringValue(cfg.LastUpdatedTimeUTC),
		SystemMetrics:      int64Map(sys),
		MetricResults:      int64Map(res),
	}
	if cfg.SchemaVersion != "" {
		base.SchemaVersion = types.StringValue(cfg.SchemaVersion)
	}
	if d.kind.isEdge() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &edgeDSModel{
			ID: base.ID, Hostname: base.Hostname, DeploymentID: base.ConfigurationID, TargetCondition: base.TargetCondition, Priority: base.Priority,
			Labels: base.Labels, ModulesContent: rawString(cfg.Content.ModulesContent), Metrics: base.Metrics, SchemaVersion: base.SchemaVersion,
			ETag: base.ETag, CreatedTimeUTC: base.CreatedTimeUTC, LastUpdatedTimeUTC: base.LastUpdatedTimeUTC, SystemMetrics: base.SystemMetrics,
			MetricResults: base.MetricResults,
		})...)
		return
	}
	base.DeviceContent = rawString(cfg.Content.DeviceContent)
	base.ModuleContent = rawString(cfg.Content.ModuleContent)
	resp.Diagnostics.Append(resp.State.Set(ctx, &base)...)
}

// rawString renders a raw content section compactly, null when absent.
func rawString(raw json.RawMessage) types.String {
	if len(raw) == 0 || string(raw) == "null" {
		return types.StringNull()
	}
	if doc, err := twinpatch.Decode(string(raw)); err == nil {
		return types.StringValue(twinpatch.Encode(doc))
	}
	return types.StringValue(string(raw))
}
