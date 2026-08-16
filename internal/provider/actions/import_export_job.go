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
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/common"
)

var (
	_ action.Action                   = &importExportJobAction{}
	_ action.ActionWithConfigure      = &importExportJobAction{}
	_ action.ActionWithValidateConfig = &importExportJobAction{}
)

// NewImportExportJobAction returns the iothub_import_export_job action.
func NewImportExportJobAction() action.Action { return &importExportJobAction{} }

type importExportJobAction struct {
	configured
}

type importExportJobModel struct {
	Type                      types.String `tfsdk:"type"`
	InputBlobContainerURI     types.String `tfsdk:"input_blob_container_uri"`
	OutputBlobContainerURI    types.String `tfsdk:"output_blob_container_uri"`
	StorageAuthenticationType types.String `tfsdk:"storage_authentication_type"`
	UserAssignedIdentity      types.String `tfsdk:"user_assigned_identity"`
	ExcludeKeysInExport       types.Bool   `tfsdk:"exclude_keys_in_export"`
	IncludeConfigurations     types.Bool   `tfsdk:"include_configurations"`
	InputBlobName             types.String `tfsdk:"input_blob_name"`
	OutputBlobName            types.String `tfsdk:"output_blob_name"`
	ConfigurationsBlobName    types.String `tfsdk:"configurations_blob_name"`
	Wait                      types.Bool   `tfsdk:"wait"`
	Timeout                   types.String `tfsdk:"timeout"`
}

const (
	quotaRetryInterval = 15 * time.Second
	rbacRetryWindow    = 5 * time.Minute
)

func (a *importExportJobAction) Metadata(_ context.Context, req action.MetadataRequest, resp *action.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_import_export_job"
}

func (a *importExportJobAction) Schema(_ context.Context, _ action.SchemaRequest, resp *action.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Runs a bulk identity registry job. An **export** writes the registry to a blob container, optionally " +
			"with configurations and deployments. An **import** reads a file in the same " +
			"[format](https://learn.microsoft.com/azure/iot-hub/iot-hub-bulk-identity-mgmt) from a container and applies each " +
			"line's `importMode` (create, update or delete) on its own. There is no rollback.\n\n" +
			"Blob access is `keyBased` with container SAS URIs, or `identityBased` with the hub's system-assigned or a " +
			"user-assigned managed identity, which needs *Storage Blob Data Contributor* on the container. A role assignment " +
			"granted moments ago can take a few minutes to become effective. The action retries during that window. SAS URIs " +
			"appear in the plan output unless they come from a sensitive variable.\n\n" +
			"~> **An import that finishes with per-line errors still counts as completed.** The hub writes them to " +
			"`importErrors.log` in the output container. The action reports where the log is but does not read it, so check " +
			"it after every import.\n\n" +
			"Only one import or export job runs per hub at a time, so the action waits for its turn within `timeout`.",
		Attributes: map[string]schema.Attribute{
			"type": schema.StringAttribute{
				MarkdownDescription: "`export` or `import`.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.OneOf(client.JobTypeExport, client.JobTypeImport)},
			},
			"input_blob_container_uri": schema.StringAttribute{
				MarkdownDescription: "Container holding the import file. Required for `import`, ignored for `export`. With `keyBased`, the container URL followed by a SAS query string with at least read and list permissions. With `identityBased`, the plain container URL.",
				Optional:            true,
			},
			"output_blob_container_uri": schema.StringAttribute{
				MarkdownDescription: "Destination container for exports and for the `importErrors.log` of imports. With `keyBased`, the container URL followed by a SAS query string with read, write, delete and list permissions. With `identityBased`, the plain container URL. The hub deletes an existing blob before writing.",
				Required:            true,
			},
			"storage_authentication_type": schema.StringAttribute{
				MarkdownDescription: "`keyBased` for SAS URIs (default), or `identityBased` for the hub's managed identity.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.OneOf(client.StorageAuthKeyBased, client.StorageAuthIdentityBased)},
			},
			"user_assigned_identity": schema.StringAttribute{
				MarkdownDescription: "Resource ID of a user-assigned managed identity of the hub, for `identityBased`. The system-assigned identity is used when omitted.",
				Optional:            true,
			},
			"exclude_keys_in_export": schema.BoolAttribute{
				MarkdownDescription: "Export without symmetric keys (default `false`). Recommended unless the export is meant to re-create the identities elsewhere. The hub then writes a plain-text warning as the first line of the export file.",
				Optional:            true,
			},
			"include_configurations": schema.BoolAttribute{
				MarkdownDescription: "Also export or import configurations and deployments, in the file named by `configurations_blob_name` (default `false`).",
				Optional:            true,
			},
			"input_blob_name":          schema.StringAttribute{MarkdownDescription: "Import file name (default `devices.txt`).", Optional: true},
			"output_blob_name":         schema.StringAttribute{MarkdownDescription: "Export file name (default `devices.txt`).", Optional: true},
			"configurations_blob_name": schema.StringAttribute{MarkdownDescription: "Configurations file name (default `configurations.txt`).", Optional: true},
			"wait":                     waitAttribute(),
			"timeout":                  timeoutAttribute("1h", "It covers waiting for a free job slot and, when `wait` is true, the job's execution. A job that outlives the deadline keeps running on the hub; cancel it with `iothub_cancel_job`."),
		},
	}
}

func (a *importExportJobAction) ValidateConfig(ctx context.Context, req action.ValidateConfigRequest, resp *action.ValidateConfigResponse) {
	var data importExportJobModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if data.Type.ValueString() == client.JobTypeImport && data.InputBlobContainerURI.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("input_blob_container_uri"), "input_blob_container_uri is required for imports", "Point it at the container holding the import file.")
	}
	if data.Type.ValueString() == client.JobTypeExport && !data.InputBlobContainerURI.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("input_blob_container_uri"), "input_blob_container_uri is not used by exports", "Remove it.")
	}
	if !data.UserAssignedIdentity.IsNull() && data.StorageAuthenticationType.ValueString() != client.StorageAuthIdentityBased {
		resp.Diagnostics.AddAttributeError(path.Root("user_assigned_identity"), "user_assigned_identity needs identityBased authentication", "Set storage_authentication_type = \"identityBased\".")
	}
}

func (a *importExportJobAction) Invoke(ctx context.Context, req action.InvokeRequest, resp *action.InvokeResponse) {
	var data importExportJobModel
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

	spec := client.ImportExportJobSpec{
		Type:                      data.Type.ValueString(),
		InputBlobContainerURI:     data.InputBlobContainerURI.ValueString(),
		OutputBlobContainerURI:    data.OutputBlobContainerURI.ValueString(),
		StorageAuthenticationType: data.StorageAuthenticationType.ValueString(),
		UserAssignedIdentity:      data.UserAssignedIdentity.ValueString(),
		ExcludeKeysInExport:       boolOr(data.ExcludeKeysInExport, false),
		IncludeConfigurations:     boolOr(data.IncludeConfigurations, false),
		InputBlobName:             data.InputBlobName.ValueString(),
		OutputBlobName:            data.OutputBlobName.ValueString(),
		ConfigurationsBlobName:    data.ConfigurationsBlobName.ValueString(),
	}
	if spec.StorageAuthenticationType == "" {
		spec.StorageAuthenticationType = client.StorageAuthKeyBased
	}
	identityBased := spec.StorageAuthenticationType == client.StorageAuthIdentityBased

	progress(resp, "Creating %s job…", spec.Type)
	tflog.Info(ctx, "creating import/export job", map[string]any{"type": spec.Type, "auth": spec.StorageAuthenticationType})
	var job *client.ImportExportJob
	started := time.Now()
	for {
		var err error
		job, err = c.CreateImportExportJob(ctx, spec)
		if err == nil {
			break
		}
		switch {
		case client.IsJobQuotaExceeded(err):
			progress(resp, "Another import/export job is running on %s; waiting for it to finish…", c.Hostname())
		case client.IsBlobContainerValidationError(err) && identityBased && time.Since(started) < rbacRetryWindow:
			progress(resp, "The hub cannot access the container yet (%s); retrying — a new role assignment for the hub's identity can take a few minutes to propagate…", firstSentence(err))
		default:
			if client.IsBlobContainerValidationError(err) {
				resp.Diagnostics.AddError("The hub cannot access the blob container",
					fmt.Sprintf("%s\n\nkeyBased: check the SAS URI (container SAS with read/write/delete/list for the output container, read/list for the input container, not expired). identityBased: the hub's managed identity needs Storage Blob Data Contributor on the container.", common.DescribeError(err)))
				return
			}
			resp.Diagnostics.AddError("Cannot create import/export job", common.DescribeError(err))
			return
		}
		if err := sleepCtx(ctx, quotaRetryInterval); err != nil {
			resp.Diagnostics.AddError("Timed out waiting to create the import/export job", "The job could not be created within `timeout`: "+common.DescribeError(err))
			return
		}
	}
	progress(resp, "Job %q created (status %s).", job.JobID, job.Status)
	if !boolOr(data.Wait, true) {
		return
	}

	last := ""
	for !job.IsTerminal() {
		if job.Status != last {
			last = job.Status
			progress(resp, "Job %q is %s (%d%%)…", job.JobID, job.Status, job.Progress)
		}
		if err := sleepCtx(ctx, pollInterval); err != nil {
			resp.Diagnostics.AddError("Timed out waiting for the import/export job",
				fmt.Sprintf("Job %q did not finish within the action's timeout (last status %q, %d%%). It keeps running on the hub; cancel it with iothub_cancel_job or raise `timeout`.", job.JobID, last, job.Progress))
			return
		}
		fresh, err := c.GetImportExportJob(ctx, job.JobID)
		if err != nil {
			resp.Diagnostics.AddError("Cannot read import/export job", common.DescribeError(err))
			return
		}
		job = fresh
	}
	switch job.Status {
	case client.JobStatusCompleted:
		if spec.Type == client.JobTypeImport {
			progress(resp, "Import job %q completed. Per-line failures, if any, are in importErrors.log in the output container (the hub reports the job as completed even then).", job.JobID)
		} else {
			progress(resp, "Export job %q completed.", job.JobID)
		}
	default:
		resp.Diagnostics.AddError(fmt.Sprintf("Import/export job %s", job.Status),
			fmt.Sprintf("Job %q ended with status %s (%d%%). %s", job.JobID, job.Status, job.Progress, job.FailureReason))
	}
}

func firstSentence(err error) string {
	if e, ok := client.AsError(err); ok && e.Message != "" {
		return e.Message
	}
	return err.Error()
}
