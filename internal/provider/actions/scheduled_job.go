package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/action/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/common"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/twin"
)

var (
	_ action.Action                   = &scheduledJobAction{}
	_ action.ActionWithConfigure      = &scheduledJobAction{}
	_ action.ActionWithValidateConfig = &scheduledJobAction{}
)

// NewScheduledJobAction returns the iothub_scheduled_job action.
func NewScheduledJobAction() action.Action { return &scheduledJobAction{} }

type scheduledJobAction struct {
	configured
}

type scheduledJobModel struct {
	Hostname                types.String `tfsdk:"hostname"`
	JobID                   types.String `tfsdk:"job_id"`
	Type                    types.String `tfsdk:"type"`
	QueryCondition          types.String `tfsdk:"query_condition"`
	StartTime               types.String `tfsdk:"start_time"`
	MaxExecutionTimeSeconds types.Int64  `tfsdk:"max_execution_time_seconds"`
	TwinPatch               types.Object `tfsdk:"twin_patch"`
	Method                  types.Object `tfsdk:"method"`
	Wait                    types.Bool   `tfsdk:"wait"`
	FailOnDeviceFailures    types.Bool   `tfsdk:"fail_on_device_failures"`
	Timeout                 types.String `tfsdk:"timeout"`
}

type twinPatchModel struct {
	Tags              twin.Document `tfsdk:"tags"`
	DesiredProperties twin.Document `tfsdk:"desired_properties"`
}

type jobMethodModel struct {
	Name                   types.String `tfsdk:"name"`
	Payload                types.String `tfsdk:"payload"`
	ResponseTimeoutSeconds types.Int64  `tfsdk:"response_timeout_seconds"`
	ConnectTimeoutSeconds  types.Int64  `tfsdk:"connect_timeout_seconds"`
}

func (a *scheduledJobAction) Metadata(_ context.Context, req action.MetadataRequest, resp *action.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_scheduled_job"
}

func (a *scheduledJobAction) Schema(_ context.Context, _ action.SchemaRequest, resp *action.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Runs a scheduled job: a twin update (`scheduleUpdateTwin`, merging tags / desired properties) or a " +
			"direct method (`scheduleDeviceMethod`) on every device matching `query_condition`, optionally at a future `start_time`. " +
			"With `wait = true` (default) the action waits for the job to finish and fails if the job failed or was cancelled — and, " +
			"with `fail_on_device_failures` (default), if any targeted device failed. Read a job's outcome later with the " +
			"`iothub_scheduled_job` data source.\n\n" +
			"A hub runs only a limited number of jobs at a time; while the slots are taken the action waits for a free one within " +
			"`timeout`. A duplicate `job_id` is an error.",
		Attributes: map[string]schema.Attribute{
			"hostname": hostnameAttribute(),
			"job_id": schema.StringAttribute{
				MarkdownDescription: "Job ID (unique per hub); generated (`tf-<random>`) when omitted.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 128)},
			},
			"type": schema.StringAttribute{
				MarkdownDescription: "`scheduleUpdateTwin` (with `twin_patch`) or `scheduleDeviceMethod` (with `method`).",
				Required:            true,
				Validators:          []validator.String{stringvalidator.OneOf(client.JobTypeScheduleUpdateTwin, client.JobTypeScheduleDeviceMethod)},
			},
			"query_condition": schema.StringAttribute{
				MarkdownDescription: "Which devices the job targets: a `WHERE` clause over `devices` (e.g. `tags.site = 'munich'`, `deviceId IN ['a','b']`) or `*` for all.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"start_time": schema.StringAttribute{
				MarkdownDescription: "RFC 3339 start time, at most 7 days ahead; the job runs immediately when omitted. " +
					"A scheduled job occupies one of the hub's job slots until it runs.",
				Optional: true,
			},
			"max_execution_time_seconds": schema.Int64Attribute{
				MarkdownDescription: "How long the hub may run the job (upper bound for devices to be reached); hub default when omitted.",
				Optional:            true,
				Validators:          []validator.Int64{int64validator.AtLeast(1)},
			},
			"twin_patch": schema.SingleNestedAttribute{
				MarkdownDescription: "For `scheduleUpdateTwin`: the tags and desired properties merged into every targeted twin (same JSON documents as `iothub_device_twin`).",
				Optional:            true,
				Attributes: map[string]schema.Attribute{
					"tags":               schema.StringAttribute{CustomType: twin.DocumentType, MarkdownDescription: "Tags to merge (JSON object).", Optional: true},
					"desired_properties": schema.StringAttribute{CustomType: twin.DocumentType, MarkdownDescription: "Desired properties to merge (JSON object).", Optional: true},
				},
			},
			"method": schema.SingleNestedAttribute{
				MarkdownDescription: "For `scheduleDeviceMethod`: the direct method to invoke on every targeted device.",
				Optional:            true,
				Attributes: map[string]schema.Attribute{
					"name":                     schema.StringAttribute{MarkdownDescription: "Method name.", Required: true, Validators: []validator.String{stringvalidator.LengthAtLeast(1)}},
					"payload":                  schema.StringAttribute{MarkdownDescription: "JSON payload (any JSON value); `null` when omitted.", Optional: true},
					"response_timeout_seconds": schema.Int64Attribute{MarkdownDescription: "Per-device response timeout, 5–300 (default 30).", Optional: true, Validators: []validator.Int64{int64validator.Between(5, 300)}},
					"connect_timeout_seconds":  schema.Int64Attribute{MarkdownDescription: "Per-device connect timeout, 0–300 (default 0).", Optional: true, Validators: []validator.Int64{int64validator.Between(0, 300)}},
				},
			},
			"wait": waitAttribute(),
			"fail_on_device_failures": schema.BoolAttribute{
				MarkdownDescription: "With `wait`, fail the apply when the job completed but some devices failed (default `true`).",
				Optional:            true,
			},
			"timeout": timeoutAttribute("1h"),
		},
	}
}

func (a *scheduledJobAction) ValidateConfig(ctx context.Context, req action.ValidateConfigRequest, resp *action.ValidateConfigResponse) {
	var data scheduledJobModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !data.Type.IsUnknown() && !data.Type.IsNull() {
		switch data.Type.ValueString() {
		case client.JobTypeScheduleUpdateTwin:
			if data.TwinPatch.IsNull() {
				resp.Diagnostics.AddAttributeError(path.Root("twin_patch"), "twin_patch is required", "A scheduleUpdateTwin job needs `twin_patch` with `tags` and/or `desired_properties`.")
			}
			if !data.Method.IsNull() {
				resp.Diagnostics.AddAttributeError(path.Root("method"), "method is not allowed", "A scheduleUpdateTwin job takes `twin_patch`, not `method`.")
			}
		case client.JobTypeScheduleDeviceMethod:
			if data.Method.IsNull() {
				resp.Diagnostics.AddAttributeError(path.Root("method"), "method is required", "A scheduleDeviceMethod job needs `method`.")
			}
			if !data.TwinPatch.IsNull() {
				resp.Diagnostics.AddAttributeError(path.Root("twin_patch"), "twin_patch is not allowed", "A scheduleDeviceMethod job takes `method`, not `twin_patch`.")
			}
		}
	}
	if !data.TwinPatch.IsNull() && !data.TwinPatch.IsUnknown() {
		var tp twinPatchModel
		resp.Diagnostics.Append(data.TwinPatch.As(ctx, &tp, objectAsOptions)...)
		if tp.Tags.IsNull() && tp.DesiredProperties.IsNull() {
			resp.Diagnostics.AddAttributeError(path.Root("twin_patch"), "Empty twin patch", "Set `tags` and/or `desired_properties`.")
		}
	}
	if !data.Method.IsNull() && !data.Method.IsUnknown() {
		var m jobMethodModel
		resp.Diagnostics.Append(data.Method.As(ctx, &m, objectAsOptions)...)
		if !m.Payload.IsNull() && !m.Payload.IsUnknown() && !json.Valid([]byte(m.Payload.ValueString())) {
			resp.Diagnostics.AddAttributeError(path.Root("method").AtName("payload"), "Invalid payload", "payload must be valid JSON (use jsonencode).")
		}
	}
	if !data.StartTime.IsNull() && !data.StartTime.IsUnknown() {
		st, err := time.Parse(time.RFC3339, data.StartTime.ValueString())
		if err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("start_time"), "Invalid start_time", "Use RFC 3339, e.g. 2026-09-01T02:00:00Z.")
		} else if time.Until(st) > maxScheduleAhead {
			resp.Diagnostics.AddAttributeError(path.Root("start_time"), "start_time too far ahead", "The hub schedules jobs at most 168 hours (7 days) ahead.")
		}
	}
	if _, d := parseTimeout(data.Timeout, defaultJobTimeout); d.HasError() {
		resp.Diagnostics.Append(d...)
	}
}

func (a *scheduledJobAction) Invoke(ctx context.Context, req action.InvokeRequest, resp *action.InvokeResponse) {
	var data scheduledJobModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := parseTimeout(data.Timeout, defaultJobTimeout)
	resp.Diagnostics.Append(diags...)
	c, d := clientFor(ctx, a.pd, data.Hostname)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	spec := client.ScheduledJobSpec{
		JobID:                   data.JobID.ValueString(),
		Type:                    data.Type.ValueString(),
		QueryCondition:          data.QueryCondition.ValueString(),
		StartTime:               data.StartTime.ValueString(),
		MaxExecutionTimeSeconds: data.MaxExecutionTimeSeconds.ValueInt64(),
	}
	if spec.JobID == "" {
		spec.JobID = newJobID("tf")
	}
	if !data.TwinPatch.IsNull() {
		var tp twinPatchModel
		resp.Diagnostics.Append(data.TwinPatch.As(ctx, &tp, objectAsOptions)...)
		var err error
		if spec.TwinPatch.Tags, err = tp.Tags.Object(); err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("twin_patch").AtName("tags"), "Invalid twin document", err.Error())
		}
		if spec.TwinPatch.Desired, err = tp.DesiredProperties.Object(); err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("twin_patch").AtName("desired_properties"), "Invalid twin document", err.Error())
		}
	}
	if !data.Method.IsNull() {
		var m jobMethodModel
		resp.Diagnostics.Append(data.Method.As(ctx, &m, objectAsOptions)...)
		mr := &client.MethodRequest{MethodName: m.Name.ValueString(), ResponseTimeoutSeconds: defaultResponseTimeout, ConnectTimeoutSeconds: defaultConnectTimeout}
		if !m.ResponseTimeoutSeconds.IsNull() {
			mr.ResponseTimeoutSeconds = m.ResponseTimeoutSeconds.ValueInt64()
		}
		if !m.ConnectTimeoutSeconds.IsNull() {
			mr.ConnectTimeoutSeconds = m.ConnectTimeoutSeconds.ValueInt64()
		}
		if !m.Payload.IsNull() {
			mr.Payload = json.RawMessage(m.Payload.ValueString())
		}
		spec.Method = mr
	}
	if resp.Diagnostics.HasError() {
		return
	}

	progress(resp, "Creating %s job %q (%s)…", spec.Type, spec.JobID, spec.QueryCondition)
	tflog.Info(ctx, "creating scheduled job", map[string]any{"job_id": spec.JobID, "type": spec.Type})
	job, err := c.CreateScheduledJob(ctx, spec)
	if err != nil {
		if client.IsConflict(err) {
			resp.Diagnostics.AddAttributeError(path.Root("job_id"), "Job already exists",
				fmt.Sprintf("A job with ID %q already exists in %s (job history is kept for 30 days). Choose another job_id or omit it to get a generated one.\n\n%s", spec.JobID, c.Hostname(), common.DescribeError(err)))
			return
		}
		resp.Diagnostics.AddError("Cannot create scheduled job", common.DescribeError(err))
		return
	}
	progress(resp, "Job %q created (status %s).", job.JobID, job.Status)
	if !boolOr(data.Wait, true) {
		return
	}

	final, diags := waitScheduledJob(ctx, c, job.JobID, resp)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	stats := "no device statistics reported"
	if s := final.DeviceJobStatistics; s != nil {
		stats = fmt.Sprintf("%d device(s): %d succeeded, %d failed, %d pending, %d running", s.DeviceCount, s.SucceededCount, s.FailedCount, s.PendingCount, s.RunningCount)
	}
	switch final.Status {
	case client.JobStatusCompleted:
		if s := final.DeviceJobStatistics; s != nil && s.FailedCount > 0 && boolOr(data.FailOnDeviceFailures, true) {
			resp.Diagnostics.AddError("Scheduled job completed with device failures",
				fmt.Sprintf("Job %q completed, but %s. Inspect the job with `data \"iothub_scheduled_job\"` or `az iot hub job show`, or set fail_on_device_failures = false to accept partial success.", final.JobID, stats))
			return
		}
		progress(resp, "Job %q completed: %s.", final.JobID, stats)
	default:
		resp.Diagnostics.AddError(fmt.Sprintf("Scheduled job %s", final.Status),
			fmt.Sprintf("Job %q ended with status %s (%s). %s %s", final.JobID, final.Status, stats, final.FailureReason, final.StatusMessage))
	}
}

// waitScheduledJob polls a job to a terminal state, reporting status changes.
func waitScheduledJob(ctx context.Context, c *client.Client, id string, resp *action.InvokeResponse) (*client.ScheduledJob, diagnostics) {
	var diags diagnostics
	last := ""
	unknown := 0
	for {
		job, err := c.GetScheduledJob(ctx, id)
		if err != nil {
			diags.AddError("Cannot read scheduled job", common.DescribeError(err))
			return nil, diags
		}
		if job.IsUnknown() {
			// A freshly created job can briefly be unknown to the read path;
			// a job that stays unknown (consecutive polls) was never created.
			if unknown++; unknown > 6 {
				diags.AddError("Scheduled job vanished", fmt.Sprintf("Job %q is unknown to %s.", id, c.Hostname()))
				return nil, diags
			}
		} else if unknown = 0; job.IsTerminal() {
			return job, diags
		} else if job.Status != last {
			last = job.Status
			msg := fmt.Sprintf("Job %q is %s", id, job.Status)
			if job.Status == client.JobStatusScheduled && job.StartTime != "" {
				msg += " (start " + job.StartTime + ")"
			}
			if s := job.DeviceJobStatistics; s != nil {
				msg += fmt.Sprintf(" — %d/%d devices done", s.SucceededCount+s.FailedCount, s.DeviceCount)
			}
			progress(resp, "%s…", msg)
		}
		if err := sleepCtx(ctx, pollInterval); err != nil {
			diags.AddError("Timed out waiting for the scheduled job",
				fmt.Sprintf("Job %q did not finish within the action's timeout (last status %q). It keeps running on the hub; cancel it with iothub_cancel_job or raise `timeout`.", id, last))
			return nil, diags
		}
	}
}
