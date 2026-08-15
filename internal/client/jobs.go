package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// ---- scheduled jobs (/jobs/v2) ------------------------------------------------

// Scheduled job types and statuses (API literals).
const (
	JobTypeScheduleUpdateTwin   = "scheduleUpdateTwin"
	JobTypeScheduleDeviceMethod = "scheduleDeviceMethod"

	JobStatusQueued    = "queued"
	JobStatusScheduled = "scheduled"
	JobStatusRunning   = "running"
	JobStatusCompleted = "completed"
	JobStatusFailed    = "failed"
	JobStatusCancelled = "cancelled"
	JobStatusUnknown   = "unknown"
	// JobStatusEnqueued is the initial status of import/export jobs.
	JobStatusEnqueued = "enqueued"
)

// ScheduledJob is a twin-update or device-method job (JobResponse).
type ScheduledJob struct {
	JobID                     string               `json:"jobId"`
	Type                      string               `json:"type"`
	Status                    string               `json:"status"`
	QueryCondition            string               `json:"queryCondition,omitempty"`
	CreatedTime               string               `json:"createdTime,omitempty"`
	StartTime                 string               `json:"startTime,omitempty"`
	EndTime                   string               `json:"endTime,omitempty"`
	MaxExecutionTimeInSeconds int64                `json:"maxExecutionTimeInSeconds,omitempty"`
	UpdateTwin                json.RawMessage      `json:"updateTwin,omitempty"`
	CloudToDeviceMethod       json.RawMessage      `json:"cloudToDeviceMethod,omitempty"`
	FailureReason             string               `json:"failureReason,omitempty"`
	StatusMessage             string               `json:"statusMessage,omitempty"`
	DeviceJobStatistics       *DeviceJobStatistics `json:"deviceJobStatistics,omitempty"`
}

// IsUnknown reports the service's answer for a job it does not know:
// GET /jobs/v2/{id} is 200 with type and status "unknown" (verified).
func (j *ScheduledJob) IsUnknown() bool { return j.Status == JobStatusUnknown }

// IsTerminal reports whether the job will not change any more.
func (j *ScheduledJob) IsTerminal() bool {
	switch j.Status {
	case JobStatusCompleted, JobStatusFailed, JobStatusCancelled:
		return true
	}
	return false
}

// DeviceJobStatistics are the per-device counters of a scheduled job.
type DeviceJobStatistics struct {
	DeviceCount    int64 `json:"deviceCount"`
	FailedCount    int64 `json:"failedCount"`
	SucceededCount int64 `json:"succeededCount"`
	RunningCount   int64 `json:"runningCount"`
	PendingCount   int64 `json:"pendingCount"`
}

// ScheduledJobSpec creates a scheduled job. Exactly one of TwinPatch and
// Method must be set, matching Type.
type ScheduledJobSpec struct {
	JobID          string
	Type           string
	QueryCondition string
	// StartTime is RFC 3339; empty starts immediately (verified).
	StartTime               string
	MaxExecutionTimeSeconds int64
	// TwinPatch is the twin merge patch (tags / properties.desired) for
	// scheduleUpdateTwin.
	TwinPatch TwinPatch
	// Method is the direct method for scheduleDeviceMethod.
	Method *MethodRequest
}

func (s ScheduledJobSpec) body() map[string]any {
	b := map[string]any{"jobId": s.JobID, "type": s.Type, "queryCondition": s.QueryCondition}
	if s.StartTime != "" {
		b["startTime"] = s.StartTime
	}
	if s.MaxExecutionTimeSeconds > 0 {
		b["maxExecutionTimeInSeconds"] = s.MaxExecutionTimeSeconds
	}
	switch s.Type {
	case JobTypeScheduleUpdateTwin:
		patch := map[string]any{"etag": "*"}
		if s.TwinPatch.Tags != nil {
			patch["tags"] = s.TwinPatch.Tags
		}
		if s.TwinPatch.Desired != nil {
			patch["properties"] = map[string]any{"desired": s.TwinPatch.Desired}
		}
		b["updateTwin"] = patch
	case JobTypeScheduleDeviceMethod:
		if s.Method != nil {
			m := *s.Method
			if m.Payload == nil {
				m.Payload = json.RawMessage("null")
			}
			b["cloudToDeviceMethod"] = m
		}
	}
	return b
}

func scheduledJobPath(id string) string { return "/jobs/v2/" + id }

// CreateScheduledJob schedules a job (PUT /jobs/v2/{id}); the answer is the
// job with status queued. A duplicate ID answers 409 JobAlreadyExists; when
// the hub's concurrent-job quota is used up it answers 429
// ThrottlingMaxActiveJobCountExceeded, which the retry policy waits out
// (verified). Creation is idempotent per ID and therefore retried normally.
func (c *Client) CreateScheduledJob(ctx context.Context, spec ScheduledJobSpec) (*ScheduledJob, error) {
	var out ScheduledJob
	if _, err := c.do(ctx, request{method: http.MethodPut, path: scheduledJobPath(spec.JobID), body: spec.body()}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetScheduledJob reads a job; use IsUnknown for a job the hub does not
// know.
func (c *Client) GetScheduledJob(ctx context.Context, id string) (*ScheduledJob, error) {
	var out ScheduledJob
	if _, err := c.do(ctx, request{method: http.MethodGet, path: scheduledJobPath(id)}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CancelScheduledJob cancels a job (POST /jobs/v2/{id}/cancel). The answer
// says cancelled but a following GET may lag; an unknown job answers 404
// JobNotFound.
func (c *Client) CancelScheduledJob(ctx context.Context, id string) (*ScheduledJob, error) {
	var out ScheduledJob
	if _, err := c.do(ctx, request{method: http.MethodPost, path: scheduledJobPath(id) + "/cancel"}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// QueryScheduledJobs lists jobs, optionally filtered by type and status,
// following continuation tokens.
func (c *Client) QueryScheduledJobs(ctx context.Context, jobType, jobStatus string) ([]ScheduledJob, error) {
	q := url.Values{}
	if jobType != "" {
		q.Set("jobType", jobType)
	}
	if jobStatus != "" {
		q.Set("jobStatus", jobStatus)
	}
	var all []ScheduledJob
	continuation := ""
	for {
		h := http.Header{}
		if continuation != "" {
			h.Set("X-Ms-Continuation", continuation)
		}
		var page []ScheduledJob
		res, err := c.do(ctx, request{method: http.MethodGet, path: "/jobs/v2/query", query: q, headers: h}, &page)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		continuation = res.Headers.Get("X-Ms-Continuation")
		if continuation == "" {
			return all, nil
		}
	}
}

// ---- import/export jobs (/jobs) --------------------------------------------------

// Import/export job types and storage authentication types.
const (
	JobTypeExport = "export"
	JobTypeImport = "import"

	StorageAuthKeyBased      = "keyBased"
	StorageAuthIdentityBased = "identityBased"
)

// ImportExportJob is a bulk registry job (JobProperties).
type ImportExportJob struct {
	JobID                     string `json:"jobId"`
	Type                      string `json:"type"`
	Status                    string `json:"status"`
	Progress                  int64  `json:"progress"`
	StartTimeUTC              string `json:"startTimeUtc,omitempty"`
	EndTimeUTC                string `json:"endTimeUtc,omitempty"`
	FailureReason             string `json:"failureReason,omitempty"`
	ExcludeKeysInExport       bool   `json:"excludeKeysInExport"`
	IncludeConfigurations     bool   `json:"includeConfigurations"`
	StorageAuthenticationType string `json:"storageAuthenticationType,omitempty"`
	OutputBlobContainerURI    string `json:"outputBlobContainerUri,omitempty"`
	InputBlobContainerURI     string `json:"inputBlobContainerUri,omitempty"`
}

// IsTerminal reports whether the job will not change any more.
func (j *ImportExportJob) IsTerminal() bool {
	switch j.Status {
	case JobStatusCompleted, JobStatusFailed, JobStatusCancelled:
		return true
	}
	return false
}

// ImportExportJobSpec creates an import or export job.
type ImportExportJobSpec struct {
	Type                      string
	InputBlobContainerURI     string // import: container with devices.txt
	OutputBlobContainerURI    string // export: destination; import: importErrors.log
	ExcludeKeysInExport       bool
	StorageAuthenticationType string // keyBased (SAS in the URIs) or identityBased
	UserAssignedIdentity      string // resource ID of a user-assigned identity, identityBased only
	IncludeConfigurations     bool
	ConfigurationsBlobName    string
	InputBlobName             string
	OutputBlobName            string
}

func (s ImportExportJobSpec) body() map[string]any {
	b := map[string]any{"type": s.Type}
	if s.InputBlobContainerURI != "" {
		b["inputBlobContainerUri"] = s.InputBlobContainerURI
	}
	if s.OutputBlobContainerURI != "" {
		b["outputBlobContainerUri"] = s.OutputBlobContainerURI
	}
	if s.Type == JobTypeExport {
		b["excludeKeysInExport"] = s.ExcludeKeysInExport
	}
	if s.StorageAuthenticationType != "" {
		b["storageAuthenticationType"] = s.StorageAuthenticationType
	}
	if s.UserAssignedIdentity != "" {
		b["identity"] = map[string]any{"userAssignedIdentity": s.UserAssignedIdentity}
	}
	if s.IncludeConfigurations {
		b["includeConfigurations"] = true
	}
	if s.ConfigurationsBlobName != "" {
		b["configurationsBlobName"] = s.ConfigurationsBlobName
	}
	if s.InputBlobName != "" {
		b["inputBlobName"] = s.InputBlobName
	}
	if s.OutputBlobName != "" {
		b["outputBlobName"] = s.OutputBlobName
	}
	return b
}

// CreateImportExportJob starts a bulk job (POST /jobs/create). Verified
// answers: 403 JobQuotaExceeded while another import/export job runs, 400
// BlobContainerValidationError for a bad SAS or a role assignment that has
// not propagated yet. The call is not re-sent after an ambiguous failure
// (a second job would be created).
func (c *Client) CreateImportExportJob(ctx context.Context, spec ImportExportJobSpec) (*ImportExportJob, error) {
	var out ImportExportJob
	r := request{method: http.MethodPost, path: "/jobs/create", body: spec.body(), retry: perRequest{OnlyThrottleRetries: true}}
	if _, err := c.do(ctx, r, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetImportExportJob reads a bulk job; unknown IDs answer 404 JobNotFound.
func (c *Client) GetImportExportJob(ctx context.Context, id string) (*ImportExportJob, error) {
	var out ImportExportJob
	if _, err := c.do(ctx, request{method: http.MethodGet, path: "/jobs/" + id}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CancelImportExportJob cancels a bulk job (DELETE /jobs/{id}); unknown IDs
// answer 404.
func (c *Client) CancelImportExportJob(ctx context.Context, id string) error {
	_, err := c.do(ctx, request{method: http.MethodDelete, path: "/jobs/" + id}, nil)
	return err
}

// ListImportExportJobs returns the hub's bulk job history.
func (c *Client) ListImportExportJobs(ctx context.Context) ([]ImportExportJob, error) {
	var out []ImportExportJob
	if _, err := c.do(ctx, request{method: http.MethodGet, path: "/jobs"}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// IsJobQuotaExceeded reports the 403 for a second concurrent import/export
// job.
func IsJobQuotaExceeded(err error) bool {
	e, ok := AsError(err)
	return ok && e.StatusCode == http.StatusForbidden && e.Code == "JobQuotaExceeded"
}

// IsBlobContainerValidationError reports the synchronous 400 for a
// container the hub cannot access.
func IsBlobContainerValidationError(err error) bool {
	e, ok := AsError(err)
	return ok && e.StatusCode == http.StatusBadRequest && e.Code == "BlobContainerValidationError"
}
