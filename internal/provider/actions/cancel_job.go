package actions

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/action/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/common"
)

var (
	_ action.Action              = &cancelJobAction{}
	_ action.ActionWithConfigure = &cancelJobAction{}
)

// NewCancelJobAction returns the iothub_cancel_job action.
func NewCancelJobAction() action.Action { return &cancelJobAction{} }

type cancelJobAction struct {
	configured
}

type cancelJobModel struct {
	JobID   types.String `tfsdk:"job_id"`
	Kind    types.String `tfsdk:"kind"`
	Timeout types.String `tfsdk:"timeout"`
}

const (
	kindScheduled    = "scheduled"
	kindImportExport = "import_export"
)

func (a *cancelJobAction) Metadata(_ context.Context, req action.MetadataRequest, resp *action.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_cancel_job"
}

func (a *cancelJobAction) Schema(_ context.Context, _ action.SchemaRequest, resp *action.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Cancels a queued or scheduled twin-update or method job, or an enqueued or running import/export job, " +
			"and waits until the hub reports a terminal state. IoT Hub does not cancel a scheduled job after it has started running. " +
			"A job that already finished is reported as such without an error. An unknown job fails.",
		Attributes: map[string]schema.Attribute{
			"job_id": schema.StringAttribute{MarkdownDescription: "Job ID.", Required: true, Validators: []validator.String{stringvalidator.LengthAtLeast(1)}},
			"kind": schema.StringAttribute{
				MarkdownDescription: "`scheduled` for twin-update and device-method jobs, or `import_export`.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.OneOf(kindScheduled, kindImportExport)},
			},
			"timeout": timeoutAttribute("5m", "It covers the cancellation request and waiting for the terminal job record."),
		},
	}
}

func (a *cancelJobAction) Invoke(ctx context.Context, req action.InvokeRequest, resp *action.InvokeResponse) {
	var data cancelJobModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := parseTimeout(data.Timeout, 5*time.Minute)
	resp.Diagnostics.Append(diags...)
	c, d := hubClient(a.pd)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	id := data.JobID.ValueString()

	if data.Kind.ValueString() == kindImportExport {
		job, err := c.GetImportExportJob(ctx, id)
		if err != nil {
			if client.IsNotFound(err) {
				resp.Diagnostics.AddAttributeError(path.Root("job_id"), "Job not found", fmt.Sprintf("No import/export job %q exists in %s.\n\n%s", id, c.Hostname(), common.DescribeError(err)))
				return
			}
			resp.Diagnostics.AddError("Cannot read import/export job", common.DescribeError(err))
			return
		}
		if job.IsTerminal() {
			progress(resp, "Import/export job %q is already %s.", id, job.Status)
			return
		}
		if err := c.CancelImportExportJob(ctx, id); err != nil {
			// Completion can race the cancellation request, and the service may
			// answer 500 rather than a useful terminal-state error. Read truth.
			fresh, readErr := c.GetImportExportJob(ctx, id)
			if readErr == nil && fresh.IsTerminal() {
				progress(resp, "Import/export job %q is already %s.", id, fresh.Status)
				return
			}
			resp.Diagnostics.AddError("Cannot cancel import/export job", common.DescribeError(err))
			return
		}
		// The cancel is normally instant; confirm it from the job record so
		// that both kinds keep the same contract (a job that vanished counts
		// as cancelled).
		for {
			job, err := c.GetImportExportJob(ctx, id)
			if err != nil {
				if client.IsNotFound(err) {
					progress(resp, "Import/export job %q cancelled.", id)
					return
				}
				resp.Diagnostics.AddError("Cannot read import/export job", common.DescribeError(err))
				return
			}
			if job.IsTerminal() {
				progress(resp, "Import/export job %q is %s.", id, job.Status)
				return
			}
			if err := sleepCtx(ctx, pollInterval); err != nil {
				resp.Diagnostics.AddError("Timed out waiting for the cancellation", fmt.Sprintf("Job %q is still %s.", id, job.Status))
				return
			}
		}
	}

	job, err := c.GetScheduledJob(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Cannot read scheduled job", common.DescribeError(err))
		return
	}
	if job.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("job_id"), "Job not found", fmt.Sprintf("No scheduled job %q exists in %s.", id, c.Hostname()))
		return
	}
	if job.IsTerminal() {
		progress(resp, "Scheduled job %q is already %s.", id, job.Status)
		return
	}
	job, err = c.CancelScheduledJob(ctx, id)
	if err != nil {
		// Completion (or the start of execution) can race the cancellation
		// request: the hub answers 405 for a running or finished job.
		fresh, readErr := c.GetScheduledJob(ctx, id)
		if readErr == nil && fresh.IsTerminal() {
			progress(resp, "Scheduled job %q is already %s.", id, fresh.Status)
			return
		}
		if readErr == nil && fresh.Status == client.JobStatusRunning {
			resp.Diagnostics.AddError("Job is already running", fmt.Sprintf("The hub does not cancel scheduled job %q once it has started running; it finishes on its own.\n\n%s", id, common.DescribeError(err)))
			return
		}
		resp.Diagnostics.AddError("Cannot cancel scheduled job", common.DescribeError(err))
		return
	}
	// The cancel answer says cancelled but the read path may lag: poll to a
	// terminal state (verified).
	for !job.IsTerminal() {
		if err := sleepCtx(ctx, pollInterval); err != nil {
			resp.Diagnostics.AddError("Timed out waiting for the cancellation", fmt.Sprintf("Job %q is still %s.", id, job.Status))
			return
		}
		if job, err = c.GetScheduledJob(ctx, id); err != nil {
			resp.Diagnostics.AddError("Cannot read scheduled job", common.DescribeError(err))
			return
		}
		if job.IsUnknown() {
			break
		}
	}
	progress(resp, "Scheduled job %q is %s.", id, job.Status)
}
