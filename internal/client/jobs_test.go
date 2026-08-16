package client

import (
	"context"
	"testing"
)

func TestScheduledJobs_CreateBodyAndStatuses(t *testing.T) {
	srv, calls := deviceServer(t, 200, `{"jobId":"j1","type":"scheduleUpdateTwin","status":"queued"}`)
	defer srv.Close()
	c, _ := newTestClient(t, srv, nil)
	job, err := c.CreateScheduledJob(context.Background(), ScheduledJobSpec{
		JobID: "j1", Type: JobTypeScheduleUpdateTwin, QueryCondition: "tags.site = 'x'", StartTime: "2027-01-01T00:00:00Z", MaxExecutionTimeSeconds: 600,
		TwinPatch: TwinPatch{Tags: map[string]any{"a": 1}, Desired: map[string]any{"b": 2}},
	})
	if err != nil || job.Status != JobStatusQueued {
		t.Fatalf("create: %v %+v", err, job)
	}
	call := (*calls)[0]
	if call.method != "PUT" || call.path != "/jobs/v2/j1" || call.body["startTime"] != "2027-01-01T00:00:00Z" || call.body["maxExecutionTimeInSeconds"] != float64(600) {
		t.Errorf("request %+v", call)
	}
	ut, _ := call.body["updateTwin"].(map[string]any)
	if ut["etag"] != "*" || ut["tags"] == nil || ut["properties"] == nil {
		t.Errorf("updateTwin %v", ut)
	}
	// method job
	_, _ = c.CreateScheduledJob(context.Background(), ScheduledJobSpec{JobID: "j2", Type: JobTypeScheduleDeviceMethod, QueryCondition: "*",
		Method: &MethodRequest{MethodName: "reboot", ResponseTimeoutSeconds: 30}})
	call = (*calls)[1]
	if _, ok := call.body["updateTwin"]; ok {
		t.Errorf("method job must not carry updateTwin: %v", call.body)
	}
	m, _ := call.body["cloudToDeviceMethod"].(map[string]any)
	if m["methodName"] != "reboot" || m["payload"] != nil {
		t.Errorf("cloudToDeviceMethod %v", m)
	}
	if _, ok := call.body["startTime"]; ok {
		t.Errorf("empty startTime must be omitted: %v", call.body)
	}

	if !(&ScheduledJob{Status: "unknown"}).IsUnknown() || (&ScheduledJob{Status: "running"}).IsTerminal() || !(&ScheduledJob{Status: "failed"}).IsTerminal() {
		t.Error("status helpers")
	}
	if _, err := c.CancelScheduledJob(context.Background(), "j1"); err != nil {
		t.Fatal(err)
	}
	if call = (*calls)[2]; call.method != "POST" || call.path != "/jobs/v2/j1/cancel" {
		t.Errorf("cancel %+v", call)
	}
}

func TestImportExportJobs(t *testing.T) {
	srv, calls := deviceServer(t, 200, `{"jobId":"x","type":"export","status":"enqueued","progress":0}`)
	defer srv.Close()
	c, _ := newTestClient(t, srv, nil)
	job, err := c.CreateImportExportJob(context.Background(), ImportExportJobSpec{
		Type: JobTypeExport, OutputBlobContainerURI: "https://acc.blob.core.windows.net/c?sig=secret", ExcludeKeysInExport: true,
		StorageAuthenticationType: StorageAuthKeyBased, IncludeConfigurations: true,
	})
	if err != nil || job.Status != JobStatusEnqueued {
		t.Fatalf("create: %v %+v", err, job)
	}
	call := (*calls)[0]
	if call.method != "POST" || call.path != "/jobs/create" || call.body["type"] != "export" || call.body["excludeKeysInExport"] != true || call.body["includeConfigurations"] != true {
		t.Errorf("request %+v", call)
	}
	if _, ok := call.body["inputBlobContainerUri"]; ok {
		t.Errorf("empty input URI must be omitted: %v", call.body)
	}
	_, _ = c.CreateImportExportJob(context.Background(), ImportExportJobSpec{Type: JobTypeImport, InputBlobContainerURI: "in", OutputBlobContainerURI: "out",
		StorageAuthenticationType: StorageAuthIdentityBased, UserAssignedIdentity: "/subscriptions/x/…/id1", InputBlobName: "d.txt"})
	call = (*calls)[1]
	if _, ok := call.body["excludeKeysInExport"]; ok {
		t.Errorf("import must not send excludeKeysInExport: %v", call.body)
	}
	if id, _ := call.body["identity"].(map[string]any); id["userAssignedIdentity"] != "/subscriptions/x/…/id1" || call.body["inputBlobName"] != "d.txt" {
		t.Errorf("identity/blob names %v", call.body)
	}
	if err := c.CancelImportExportJob(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	if call = (*calls)[2]; call.method != "DELETE" || call.path != "/jobs/x" {
		t.Errorf("cancel %+v", call)
	}

	quota, _ := deviceServer(t, 403, `{"Message":"ErrorCode:JobQuotaExceeded;x"}`)
	defer quota.Close()
	c2, _ := newTestClient(t, quota, nil)
	if _, err := c2.CreateImportExportJob(context.Background(), ImportExportJobSpec{Type: JobTypeExport, OutputBlobContainerURI: "o"}); !IsJobQuotaExceeded(err) {
		t.Errorf("expected JobQuotaExceeded, got %v", err)
	}
	blob, _ := deviceServer(t, 400, `{"Message":"ErrorCode:BlobContainerValidationError;Unauthorized to write to output blob container"}`)
	defer blob.Close()
	c3, _ := newTestClient(t, blob, nil)
	if _, err := c3.CreateImportExportJob(context.Background(), ImportExportJobSpec{Type: JobTypeExport, OutputBlobContainerURI: "o"}); !IsBlobContainerValidationError(err) {
		t.Errorf("expected BlobContainerValidationError, got %v", err)
	}
}
