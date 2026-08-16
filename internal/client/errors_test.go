package client

import (
	"net/http"
	"net/url"
	"testing"
)

func fakeResp(status int, hdr map[string]string, method, rawURL string) *http.Response {
	u, _ := url.Parse(rawURL)
	h := http.Header{}
	for k, v := range hdr {
		h.Set(k, v)
	}
	return &http.Response{StatusCode: status, Header: h, Request: &http.Request{Method: method, URL: u}}
}

func TestNewError_Envelopes(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		hdr        map[string]string
		body       string
		wantCode   string
		wantMsg    string
		wantTrack  string
		wantOp     string
		wantString string
	}{
		{
			name:   "envelope 1 with ErrorCode prefix and tracking id",
			status: 409, hdr: map[string]string{"iothub-errorcode": "DeviceAlreadyExists", "x-ms-request-id": "req-1"},
			body:      `{"Message":"ErrorCode:DeviceAlreadyExists;A device with ID 'tf-dev-01' is already registered.","ExceptionMessage":"Tracking ID:28F7C9FD8821474CAC76FDD9FC6AFE77-G2:-TimeStamp:2026-08-15T09:07:36.757669697Z"}`,
			wantCode:  "DeviceAlreadyExists",
			wantMsg:   "A device with ID 'tf-dev-01' is already registered.",
			wantTrack: "28F7C9FD8821474CAC76FDD9FC6AFE77-G2:-TimeStamp:2026-08-15T09:07:36.757669697Z",
		},
		{
			name:   "envelope 1 without header takes code from body",
			status: 412, hdr: nil,
			body:     `{"Message":"ErrorCode:PreconditionFailed;PreconditionFailed, ETag may be invalid.","ExceptionMessage":"Tracking ID:abc-G:0-TimeStamp:08/15/2026 09:07:13"}`,
			wantCode: "PreconditionFailed",
			wantMsg:  "PreconditionFailed, ETag may be invalid.",
		},
		{
			name:   "envelope 2",
			status: 409, hdr: map[string]string{"iothub-errorcode": "ModuleAlreadyExistsOnDevice"},
			body:      `{"errorCode":409301,"message":"Device tf-dev-01 in IotHub tf-provider-dev already contains Module telemetry.","trackingId":"985ADF09E4114051-G2:-TimeStamp:2026-08-15T09:09:19Z","timestampUtc":"2026-08-15T09:09:19Z"}`,
			wantCode:  "ModuleAlreadyExistsOnDevice",
			wantMsg:   "Device tf-dev-01 in IotHub tf-provider-dev already contains Module telemetry.",
			wantTrack: "985ADF09E4114051-G2:-TimeStamp:2026-08-15T09:09:19Z",
		},
		{
			name:   "envelope 2 nested inside envelope 1 (direct method 404)",
			status: 404, hdr: map[string]string{"iothub-errorcode": "DeviceNotOnline"},
			body:      `{"Message":"{\"errorCode\":404103,\"message\":\"The operation failed because the requested device isn't online. To learn more, see https://aka.ms/iothub404103\",\"trackingId\":\"8D951E93-G2:-TimeStamp:2026-08-15T09:11:06Z\",\"timestampUtc\":\"2026-08-15T09:11:06Z\",\"info\":null}","ExceptionMessage":""}`,
			wantCode:  "DeviceNotOnline",
			wantMsg:   "The operation failed because the requested device isn't online. To learn more, see https://aka.ms/iothub404103",
			wantTrack: "8D951E93-G2:-TimeStamp:2026-08-15T09:11:06Z",
		},
		{
			name:   "throttling message with operation type",
			status: 429, hdr: map[string]string{"iothub-errorcode": "ThrottlingBacklogTimeout"},
			body:     `{"Message":"ErrorCode:ThrottlingBacklogTimeout;The request has been throttled. Wait 10 seconds and try again. Operation type: ConfigurationWrite","ExceptionMessage":"Tracking ID:6a61b5b5-G:0-TimeStamp:08/15/2026 09:19:06"}`,
			wantCode: "ThrottlingBacklogTimeout",
			wantMsg:  "The request has been throttled. Wait 10 seconds and try again. Operation type: ConfigurationWrite",
			wantOp:   "ConfigurationWrite",
		},
		{
			name:   "multi-line job message keeps the Message: line",
			status: 403, hdr: map[string]string{"iothub-errorcode": "JobQuotaExceeded"},
			body:     "{\"Message\":\"ErrorCode:JobQuotaExceeded;Error: 403 ErrorCode: JobQuotaExceeded\\nMessage: Job quota has been exceeded\\nTimestamp: 08/15/2026 09:38:50\\nTracking ID: \",\"ExceptionMessage\":\"\"}",
			wantCode: "JobQuotaExceeded",
			wantMsg:  "Job quota has been exceeded",
		},
		{
			name:   "empty body falls back to status text",
			status: 502, hdr: nil, body: "",
			wantMsg: "Bad Gateway",
		},
		{
			name:   "non-json body is kept verbatim",
			status: 400, hdr: nil, body: "plain text failure",
			wantMsg: "plain text failure",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newError(fakeResp(tc.status, tc.hdr, "PUT", "https://h.azure-devices.net/devices/x?api-version=2021-04-12&sig=SECRET"), []byte(tc.body))
			if e.StatusCode != tc.status {
				t.Errorf("status = %d, want %d", e.StatusCode, tc.status)
			}
			if e.Code != tc.wantCode {
				t.Errorf("code = %q, want %q", e.Code, tc.wantCode)
			}
			if e.Message != tc.wantMsg {
				t.Errorf("message = %q, want %q", e.Message, tc.wantMsg)
			}
			if tc.wantTrack != "" && e.TrackingID != tc.wantTrack {
				t.Errorf("tracking = %q, want %q", e.TrackingID, tc.wantTrack)
			}
			if e.Operation != tc.wantOp {
				t.Errorf("operation = %q, want %q", e.Operation, tc.wantOp)
			}
			if e.URL != "https://h.azure-devices.net/devices/x" {
				t.Errorf("URL must be stripped of its query string, got %q", e.URL)
			}
		})
	}
}

func TestErrorPredicates(t *testing.T) {
	mk := func(s int) error { return newError(fakeResp(s, nil, "GET", "https://h/x"), nil) }
	if !IsNotFound(mk(404)) || IsNotFound(mk(409)) {
		t.Error("IsNotFound")
	}
	if !IsConflict(mk(409)) || !IsPreconditionFailed(mk(412)) {
		t.Error("IsConflict/IsPreconditionFailed")
	}
	if !IsUnauthorized(mk(401)) || !IsUnauthorized(mk(403)) || IsUnauthorized(mk(404)) {
		t.Error("IsUnauthorized")
	}
	if e, ok := AsError(mk(500)); !ok || e.StatusCode != 500 {
		t.Error("AsError")
	}
}
