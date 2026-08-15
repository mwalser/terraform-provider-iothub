// Package jobs implements the iothub_scheduled_job and
// iothub_import_export_job data sources (CONCEPT.md §7): read-only views of
// the hub's job history.
package jobs

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/common"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/identity"
	"github.com/mwalser/terraform-provider-iothub/internal/twinpatch"
)

var (
	_ datasource.DataSource              = &scheduledJobDataSource{}
	_ datasource.DataSourceWithConfigure = &scheduledJobDataSource{}
	_ datasource.DataSource              = &importExportJobDataSource{}
	_ datasource.DataSourceWithConfigure = &importExportJobDataSource{}
)

// NewScheduledJobDataSource returns the iothub_scheduled_job data source.
func NewScheduledJobDataSource() datasource.DataSource { return &scheduledJobDataSource{} }

// NewImportExportJobDataSource returns the iothub_import_export_job data source.
func NewImportExportJobDataSource() datasource.DataSource { return &importExportJobDataSource{} }

// resolve resolves the hub for a data source read; ok=false when deferred.
func resolve(ctx context.Context, pd *common.ProviderData, hostname types.String, req datasource.ReadRequest, resp *datasource.ReadResponse) (*client.Client, bool) {
	host, ok, diags := common.ResolveHostname(hostname, pd)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return nil, false
	}
	if !ok {
		if req.ClientCapabilities.DeferralAllowed {
			resp.Deferred = &datasource.Deferred{Reason: datasource.DeferredReasonProviderConfigUnknown}
		}
		return nil, false
	}
	c, diags := pd.ClientFor(ctx, host)
	resp.Diagnostics.Append(diags...)
	return c, !resp.Diagnostics.HasError()
}

// ---- scheduled job ----------------------------------------------------------------

type scheduledJobDataSource struct {
	pd *common.ProviderData
}

type scheduledJobModel struct {
	ID                      types.String `tfsdk:"id"`
	Hostname                types.String `tfsdk:"hostname"`
	JobID                   types.String `tfsdk:"job_id"`
	Type                    types.String `tfsdk:"type"`
	Status                  types.String `tfsdk:"status"`
	QueryCondition          types.String `tfsdk:"query_condition"`
	CreatedTime             types.String `tfsdk:"created_time"`
	StartTime               types.String `tfsdk:"start_time"`
	EndTime                 types.String `tfsdk:"end_time"`
	MaxExecutionTimeSeconds types.Int64  `tfsdk:"max_execution_time_seconds"`
	TwinPatch               types.String `tfsdk:"twin_patch"`
	Method                  types.String `tfsdk:"method"`
	FailureReason           types.String `tfsdk:"failure_reason"`
	StatusMessage           types.String `tfsdk:"status_message"`
	DeviceJobStatistics     types.Object `tfsdk:"device_job_statistics"`
}

var statsAttrTypes = map[string]attr.Type{
	"device_count": types.Int64Type, "failed_count": types.Int64Type, "succeeded_count": types.Int64Type,
	"running_count": types.Int64Type, "pending_count": types.Int64Type,
}

func (d *scheduledJobDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_scheduled_job"
}

func (d *scheduledJobDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	c := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{MarkdownDescription: desc, Computed: true}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A scheduled twin-update or device-method job, e.g. one started by the `iothub_scheduled_job` action. " +
			"The hub keeps job history for a limited time; an unknown job is an error.",
		Attributes: map[string]schema.Attribute{
			"id":                         c("`<hostname>/jobs/v2/<job_id>`."),
			"hostname":                   schema.StringAttribute{MarkdownDescription: common.HostnameAttributeDescription, Optional: true, Computed: true, Validators: common.HostnameValidators()},
			"job_id":                     schema.StringAttribute{MarkdownDescription: "Job ID.", Required: true},
			"type":                       c("`scheduleUpdateTwin` or `scheduleDeviceMethod`."),
			"status":                     c("`queued`, `scheduled`, `running`, `completed`, `failed` or `cancelled`."),
			"query_condition":            c("Target condition."),
			"created_time":               c("Creation time."),
			"start_time":                 c("Start time."),
			"end_time":                   c("End time (a far-future placeholder while running)."),
			"max_execution_time_seconds": schema.Int64Attribute{MarkdownDescription: "Maximum execution time.", Computed: true},
			"twin_patch":                 c("The twin patch of a `scheduleUpdateTwin` job as JSON (`{\"tags\":…,\"properties\":{\"desired\":…}}`), null otherwise."),
			"method":                     c("The method of a `scheduleDeviceMethod` job as JSON, null otherwise."),
			"failure_reason":             c("Failure reason, if any."),
			"status_message":             c("Status message, if any."),
			"device_job_statistics": schema.SingleNestedAttribute{
				MarkdownDescription: "Per-device counters (null until the hub reports them).",
				Computed:            true,
				Attributes: map[string]schema.Attribute{
					"device_count":    schema.Int64Attribute{Computed: true, MarkdownDescription: "Targeted devices."},
					"failed_count":    schema.Int64Attribute{Computed: true, MarkdownDescription: "Failed devices."},
					"succeeded_count": schema.Int64Attribute{Computed: true, MarkdownDescription: "Succeeded devices."},
					"running_count":   schema.Int64Attribute{Computed: true, MarkdownDescription: "Running devices."},
					"pending_count":   schema.Int64Attribute{Computed: true, MarkdownDescription: "Pending devices."},
				},
			},
		},
	}
}

func (d *scheduledJobDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	pd, diags := common.ExpectProviderData(req.ProviderData)
	resp.Diagnostics.Append(diags...)
	d.pd = pd
}

func (d *scheduledJobDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data scheduledJobModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, ok := resolve(ctx, d.pd, data.Hostname, req, resp)
	if !ok {
		return
	}
	job, err := c.GetScheduledJob(ctx, data.JobID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Cannot read scheduled job", common.DescribeError(err))
		return
	}
	if job.IsUnknown() {
		resp.Diagnostics.AddError("Scheduled job not found", fmt.Sprintf("No scheduled job %q is known to %s (history is kept for 30 days).", data.JobID.ValueString(), c.Hostname()))
		return
	}
	data.ID = types.StringValue(common.ResourceID(c.Hostname(), "jobs", "v2", job.JobID))
	data.Hostname = types.StringValue(c.Hostname())
	data.Type = types.StringValue(job.Type)
	data.Status = types.StringValue(job.Status)
	data.QueryCondition = identity.StringOrNull(job.QueryCondition)
	data.CreatedTime = identity.TimeOrNull(job.CreatedTime)
	data.StartTime = identity.TimeOrNull(job.StartTime)
	data.EndTime = identity.TimeOrNull(job.EndTime)
	data.MaxExecutionTimeSeconds = types.Int64Value(job.MaxExecutionTimeInSeconds)
	data.TwinPatch = rawJSON(job.UpdateTwin)
	data.Method = rawJSON(job.CloudToDeviceMethod)
	data.FailureReason = identity.StringOrNull(job.FailureReason)
	data.StatusMessage = identity.StringOrNull(job.StatusMessage)
	data.DeviceJobStatistics = types.ObjectNull(statsAttrTypes)
	if s := job.DeviceJobStatistics; s != nil {
		data.DeviceJobStatistics = types.ObjectValueMust(statsAttrTypes, map[string]attr.Value{
			"device_count": types.Int64Value(s.DeviceCount), "failed_count": types.Int64Value(s.FailedCount), "succeeded_count": types.Int64Value(s.SucceededCount),
			"running_count": types.Int64Value(s.RunningCount), "pending_count": types.Int64Value(s.PendingCount),
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func rawJSON(raw []byte) types.String {
	if len(raw) == 0 || string(raw) == "null" {
		return types.StringNull()
	}
	if doc, err := twinpatch.Decode(string(raw)); err == nil {
		return types.StringValue(twinpatch.Encode(doc))
	}
	return types.StringValue(string(raw))
}

// ---- import/export job -----------------------------------------------------------------

type importExportJobDataSource struct {
	pd *common.ProviderData
}

type importExportJobModel struct {
	ID                        types.String `tfsdk:"id"`
	Hostname                  types.String `tfsdk:"hostname"`
	JobID                     types.String `tfsdk:"job_id"`
	Type                      types.String `tfsdk:"type"`
	Status                    types.String `tfsdk:"status"`
	Progress                  types.Int64  `tfsdk:"progress"`
	StartTime                 types.String `tfsdk:"start_time"`
	EndTime                   types.String `tfsdk:"end_time"`
	FailureReason             types.String `tfsdk:"failure_reason"`
	ExcludeKeysInExport       types.Bool   `tfsdk:"exclude_keys_in_export"`
	IncludeConfigurations     types.Bool   `tfsdk:"include_configurations"`
	StorageAuthenticationType types.String `tfsdk:"storage_authentication_type"`
}

func (d *importExportJobDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_import_export_job"
}

func (d *importExportJobDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	c := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{MarkdownDescription: desc, Computed: true}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A bulk import/export job, e.g. one started by the `iothub_import_export_job` action. " +
			"The job record does not include the container URIs.",
		Attributes: map[string]schema.Attribute{
			"id":                          c("`<hostname>/jobs/<job_id>`."),
			"hostname":                    schema.StringAttribute{MarkdownDescription: common.HostnameAttributeDescription, Optional: true, Computed: true, Validators: common.HostnameValidators()},
			"job_id":                      schema.StringAttribute{MarkdownDescription: "Job ID.", Required: true},
			"type":                        c("`export` or `import`."),
			"status":                      c("`enqueued`, `running`, `completed`, `failed` or `cancelled`."),
			"progress":                    schema.Int64Attribute{MarkdownDescription: "Progress in percent.", Computed: true},
			"start_time":                  c("Start time."),
			"end_time":                    c("End time."),
			"failure_reason":              c("Failure reason, if any. Per-line import errors are not reported here; the hub writes them to `importErrors.log` in the output container."),
			"exclude_keys_in_export":      schema.BoolAttribute{MarkdownDescription: "Whether keys were excluded from the export.", Computed: true},
			"include_configurations":      schema.BoolAttribute{MarkdownDescription: "Whether configurations were included.", Computed: true},
			"storage_authentication_type": c("`keyBased` or `identityBased`, when reported."),
		},
	}
}

func (d *importExportJobDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	pd, diags := common.ExpectProviderData(req.ProviderData)
	resp.Diagnostics.Append(diags...)
	d.pd = pd
}

func (d *importExportJobDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data importExportJobModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, ok := resolve(ctx, d.pd, data.Hostname, req, resp)
	if !ok {
		return
	}
	job, err := c.GetImportExportJob(ctx, data.JobID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.Diagnostics.AddError("Import/export job not found", fmt.Sprintf("No import/export job %q exists in %s.", data.JobID.ValueString(), c.Hostname()))
			return
		}
		resp.Diagnostics.AddError("Cannot read import/export job", common.DescribeError(err))
		return
	}
	data.ID = types.StringValue(common.ResourceID(c.Hostname(), "jobs", job.JobID))
	data.Hostname = types.StringValue(c.Hostname())
	data.Type = types.StringValue(job.Type)
	data.Status = types.StringValue(job.Status)
	data.Progress = types.Int64Value(job.Progress)
	data.StartTime = identity.TimeOrNull(job.StartTimeUTC)
	data.EndTime = identity.TimeOrNull(job.EndTimeUTC)
	data.FailureReason = identity.StringOrNull(job.FailureReason)
	data.ExcludeKeysInExport = types.BoolValue(job.ExcludeKeysInExport)
	data.IncludeConfigurations = types.BoolValue(job.IncludeConfigurations)
	data.StorageAuthenticationType = identity.StringOrNull(job.StorageAuthenticationType)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
