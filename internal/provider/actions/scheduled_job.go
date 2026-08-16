package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/action/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/common"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/jsondoc"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/twin"
)

var (
	_ action.Action                   = &scheduledJobAction{}
	_ action.ActionWithConfigure      = &scheduledJobAction{}
	_ action.ActionWithValidateConfig = &scheduledJobAction{}
	_ action.ActionWithModifyPlan     = &scheduledJobAction{}
)

// NewScheduledJobAction returns the iothub_scheduled_job action.
func NewScheduledJobAction() action.Action { return &scheduledJobAction{} }

type scheduledJobAction struct {
	configured
}

type scheduledJobModel struct {
	JobID                   types.String `tfsdk:"job_id"`
	QueryCondition          types.String `tfsdk:"query_condition"`
	StartTime               types.String `tfsdk:"start_time"`
	MaxExecutionTimeSeconds types.Int64  `tfsdk:"max_execution_time_seconds"`
	TwinPatch               types.Object `tfsdk:"twin_patch"`
	Method                  types.Object `tfsdk:"method"`
	Wait                    types.Bool   `tfsdk:"wait"`
	FailOnDeviceFailures    types.Bool   `tfsdk:"fail_on_device_failures"`
	Timeout                 types.String `tfsdk:"timeout"`
}

// maxScheduleAhead is the hub's limit for a scheduled job's start_time
// (verified: "Must be within 168 hours").
const maxScheduleAhead = 168 * time.Hour

// scheduledJobIDPattern is the hub's job ID charset (verified: uppercase,
// underscores, dots and other punctuation are rejected; 64 characters at most).
var scheduledJobIDPattern = regexp.MustCompile(`^[a-z0-9\-']{1,64}$`)

type twinPatchModel struct {
	Tags              jsondoc.Value `tfsdk:"tags"`
	DesiredProperties jsondoc.Value `tfsdk:"desired_properties"`
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
		MarkdownDescription: "Runs a scheduled job on every device that matches `query_condition`, now or at a future `start_time`: " +
			"`twin_patch` merges tags and desired properties into the twins, `method` invokes a direct method. The " +
			"`iothub_scheduled_job` data source reads a job's outcome later.\n\n" +
			"A hub runs only a [limited number of jobs](https://learn.microsoft.com/azure/iot-hub/iot-hub-devguide-quotas-throttling) " +
			"at a time. While all slots are taken, the action waits for a free one within `timeout`. A `job_id` cannot be reused " +
			"while the hub still remembers the earlier job: use a new ID per run, or omit it.",
		Attributes: map[string]schema.Attribute{
			"job_id": schema.StringAttribute{
				MarkdownDescription: "Job ID, unique per hub: 1 to 64 lowercase letters, digits and hyphens. Generated as `tf-<random>` " +
					"when omitted and shown in the apply output, so you can read or cancel the job later.",
				Optional:   true,
				Validators: []validator.String{stringvalidator.RegexMatches(scheduledJobIDPattern, "must be 1 to 64 lowercase letters, digits and hyphens")},
			},
			"query_condition": schema.StringAttribute{
				MarkdownDescription: "Which devices the job targets: a `WHERE` clause over `devices`, for example `tags.site = 'munich'` or `deviceId IN ['a','b']`. Use `*` for all devices.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"start_time": schema.StringAttribute{
				MarkdownDescription: "Start time in RFC 3339 format, at most 7 days ahead. The job runs immediately when omitted. " +
					"A scheduled job occupies one of the hub's job slots until it runs.",
				Optional: true,
			},
			"max_execution_time_seconds": schema.Int64Attribute{
				MarkdownDescription: "How long the hub may run the job, in seconds. Devices not reached in time count as failed. When omitted, the hub applies its own default; set it explicitly if devices may be offline for long.",
				Optional:            true,
				Validators:          []validator.Int64{int64validator.AtLeast(1)},
			},
			"twin_patch": schema.SingleNestedAttribute{
				MarkdownDescription: "The tags and desired properties merged into every targeted twin. Same JSON documents as `iothub_device_twin`. Exactly one of `twin_patch` and `method` must be set.",
				Optional:            true,
				Attributes: map[string]schema.Attribute{
					"tags":               schema.StringAttribute{CustomType: twin.PatchDocumentType, MarkdownDescription: "Tags to merge, as a JSON object. Set a value to `null` to remove it from the twins.", Optional: true},
					"desired_properties": schema.StringAttribute{CustomType: twin.PatchDocumentType, MarkdownDescription: "Desired properties to merge, as a JSON object. Set a value to `null` to remove it from the twins.", Optional: true},
				},
			},
			"method": schema.SingleNestedAttribute{
				MarkdownDescription: "The direct method to invoke on every targeted device. Exactly one of `twin_patch` and `method` must be set.",
				Optional:            true,
				Attributes: map[string]schema.Attribute{
					"name":                     schema.StringAttribute{MarkdownDescription: "Method name.", Required: true, Validators: []validator.String{stringvalidator.LengthAtLeast(1)}},
					"payload":                  schema.StringAttribute{MarkdownDescription: "JSON payload, any JSON value (use `jsonencode`). Sent as `null` when omitted.", Optional: true},
					"response_timeout_seconds": schema.Int64Attribute{MarkdownDescription: "Per-device response timeout in seconds, 5 to 300 (default 30).", Optional: true, Validators: []validator.Int64{int64validator.Between(5, 300)}},
					"connect_timeout_seconds":  schema.Int64Attribute{MarkdownDescription: "Per-device connect timeout in seconds, 0 to 300 (default 0) and at most `response_timeout_seconds`.", Optional: true, Validators: []validator.Int64{int64validator.Between(0, 300)}},
				},
			},
			"wait": waitAttribute(),
			"fail_on_device_failures": schema.BoolAttribute{
				MarkdownDescription: "With `wait`, fail the apply when the job completed but some devices failed (default `true`).",
				Optional:            true,
			},
			"timeout": timeoutAttribute("1h", "It covers waiting for a free job slot, waiting for the scheduled start, and the job's execution when `wait` is true. A job that outlives the deadline keeps running on the hub; cancel it with `iothub_cancel_job`."),
		},
	}
}

func (a *scheduledJobAction) ValidateConfig(ctx context.Context, req action.ValidateConfigRequest, resp *action.ValidateConfigResponse) {
	resp.Diagnostics.Append(a.validate(ctx, req.Config)...)
}

// ModifyPlan repeats the validation at plan time, when values such as
// `start_time = timeadd(plantimestamp(), …)` are known.
func (a *scheduledJobAction) ModifyPlan(ctx context.Context, req action.ModifyPlanRequest, resp *action.ModifyPlanResponse) {
	resp.Diagnostics.Append(a.validate(ctx, req.Config)...)
}

func (a *scheduledJobAction) validate(ctx context.Context, cfg tfsdk.Config) diag.Diagnostics {
	var diags diag.Diagnostics
	var data scheduledJobModel
	diags.Append(cfg.Get(ctx, &data)...)
	if diags.HasError() {
		return diags
	}
	switch {
	case data.TwinPatch.IsNull() && data.Method.IsNull():
		diags.AddError("Missing job content", "Set `twin_patch` to update twins or `method` to invoke a direct method.")
	case !data.TwinPatch.IsNull() && !data.TwinPatch.IsUnknown() && !data.Method.IsNull() && !data.Method.IsUnknown():
		diags.AddAttributeError(path.Root("method"), "Only one of twin_patch and method", "A job either updates twins (`twin_patch`) or invokes a method (`method`), not both.")
	}
	if !data.TwinPatch.IsNull() && !data.TwinPatch.IsUnknown() {
		var tp twinPatchModel
		diags.Append(data.TwinPatch.As(ctx, &tp, objectAsOptions)...)
		if tp.Tags.IsNull() && tp.DesiredProperties.IsNull() {
			diags.AddAttributeError(path.Root("twin_patch"), "Empty twin patch", "Set `tags` and/or `desired_properties`.")
		}
	}
	if !data.Method.IsNull() && !data.Method.IsUnknown() {
		var m jobMethodModel
		diags.Append(data.Method.As(ctx, &m, objectAsOptions)...)
		if !m.Payload.IsNull() && !m.Payload.IsUnknown() && !json.Valid([]byte(m.Payload.ValueString())) {
			diags.AddAttributeError(path.Root("method").AtName("payload"), "Invalid payload", "payload must be valid JSON (use jsonencode).")
		}
		diags.Append(validateMethodTimeouts(m.ResponseTimeoutSeconds, m.ConnectTimeoutSeconds, path.Root("method").AtName("connect_timeout_seconds"))...)
	}
	timeout, d := parseTimeout(data.Timeout, defaultJobTimeout)
	if !data.StartTime.IsNull() && !data.StartTime.IsUnknown() {
		st, err := time.Parse(time.RFC3339, data.StartTime.ValueString())
		switch {
		case err != nil:
			diags.AddAttributeError(path.Root("start_time"), "Invalid start_time", "Use RFC 3339, e.g. 2026-09-01T02:00:00Z.")
		case time.Until(st) > maxScheduleAhead:
			diags.AddAttributeError(path.Root("start_time"), "start_time too far ahead", "The hub schedules jobs at most 168 hours (7 days) ahead.")
		case boolOr(data.Wait, true) && !d.HasError() && time.Until(st) > timeout:
			diags.AddAttributeError(path.Root("wait"), "wait would time out before the job starts",
				fmt.Sprintf("start_time is %s away but timeout is %s. Set `wait = false` to schedule the job without waiting, or raise `timeout`.",
					time.Until(st).Round(time.Minute), timeout))
		}
	}
	return diags
}

func (a *scheduledJobAction) Invoke(ctx context.Context, req action.InvokeRequest, resp *action.InvokeResponse) {
	var data scheduledJobModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := parseTimeout(data.Timeout, defaultJobTimeout)
	resp.Diagnostics.Append(diags...)
	c, d := hubClient(a.pd)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	spec := client.ScheduledJobSpec{
		JobID:                   data.JobID.ValueString(),
		Type:                    client.JobTypeScheduleUpdateTwin,
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
		spec.Type = client.JobTypeScheduleDeviceMethod
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
func waitScheduledJob(ctx context.Context, c *client.Client, id string, resp *action.InvokeResponse) (*client.ScheduledJob, diag.Diagnostics) {
	var diags diag.Diagnostics
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
