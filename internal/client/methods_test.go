package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestMethods_InvokeAndErrors(t *testing.T) {
	srv, calls := deviceServer(t, 200, `{"status":200,"payload":{"ok":true}}`)
	defer srv.Close()
	c, _ := newTestClient(t, srv, nil)
	res, err := c.InvokeDeviceMethod(context.Background(), "d1", MethodRequest{MethodName: "reboot", Payload: json.RawMessage(`{"delay":5}`), ResponseTimeoutSeconds: 30, ConnectTimeoutSeconds: 10})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != 200 || string(res.Payload) != `{"ok":true}` {
		t.Errorf("result %+v", res)
	}
	call := (*calls)[0]
	if call.method != "POST" || call.path != "/twins/d1/methods" || call.body["methodName"] != "reboot" || call.body["responseTimeoutInSeconds"] != float64(30) || call.body["connectTimeoutInSeconds"] != float64(10) {
		t.Errorf("request %+v", call)
	}
	// nil payload is sent as JSON null (the service requires the key)
	_, _ = c.InvokeModuleMethod(context.Background(), "d1", "m1", MethodRequest{MethodName: "ping", ResponseTimeoutSeconds: 5})
	call = (*calls)[1]
	if v, ok := call.body["payload"]; !ok || v != nil || call.path != "/twins/d1/modules/m1/methods" {
		t.Errorf("module request %+v", call)
	}

	// offline device: nested envelope 2 with 404103
	srv2, _ := deviceServer(t, 404, `{"Message":"{\"errorCode\":404103,\"message\":\"The operation failed because the requested device isn't online.\",\"trackingId\":\"t\"}","ExceptionMessage":""}`)
	defer srv2.Close()
	c2, _ := newTestClient(t, srv2, nil)
	_, err = c2.InvokeDeviceMethod(context.Background(), "d1", MethodRequest{MethodName: "x", ResponseTimeoutSeconds: 5})
	if !IsDeviceNotOnline(err) || !IsNotFound(err) {
		t.Errorf("expected DeviceNotOnline, got %v", err)
	}
	e, _ := AsError(err)
	if e.Message != "The operation failed because the requested device isn't online." {
		t.Errorf("message %q", e.Message)
	}
}

func TestMethods_NoRetryOn5xx_ButRetryOn429(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&n, 1) == 1 {
			w.WriteHeader(500)
			_, _ = w.Write([]byte(`{"Message":"ErrorCode:ServerError;boom"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":200}`))
	}))
	defer srv.Close()
	c, _ := newTestClient(t, srv, nil)
	if _, err := c.InvokeDeviceMethod(context.Background(), "d1", MethodRequest{MethodName: "x", ResponseTimeoutSeconds: 5}); err == nil {
		t.Fatal("a 500 on a direct method must not be retried (the method may have run)")
	}
	if atomic.LoadInt32(&n) != 1 {
		t.Errorf("attempts = %d, want 1", n)
	}
	// but 429 (not processed) is retried
	atomic.StoreInt32(&n, 0)
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&n, 1) == 1 {
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"Message":"ErrorCode:ThrottlingBacklogTimeout;Wait 10 seconds"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":200}`))
	}))
	defer srv2.Close()
	c2, _ := newTestClient(t, srv2, nil)
	if _, err := c2.InvokeDeviceMethod(context.Background(), "d1", MethodRequest{MethodName: "x", ResponseTimeoutSeconds: 5}); err != nil {
		t.Fatalf("429 must be retried: %v", err)
	}
	if atomic.LoadInt32(&n) != 2 {
		t.Errorf("attempts = %d, want 2", n)
	}
}

func TestMethods_ApplyAndPurge(t *testing.T) {
	srv, calls := deviceServer(t, 200, `{"deviceId":"d1","totalMessagesPurged":3}`)
	defer srv.Close()
	c, _ := newTestClient(t, srv, nil)
	res, err := c.PurgeCloudToDeviceQueue(context.Background(), "d1")
	if err != nil || res.TotalMessagesPurged != 3 {
		t.Fatalf("purge: %v %+v", err, res)
	}
	if call := (*calls)[0]; call.method != "DELETE" || call.path != "/devices/d1/commands" {
		t.Errorf("purge request %+v", call)
	}
	if err := c.ApplyConfigurationContent(context.Background(), "e1", json.RawMessage(`{"$edgeAgent":{}}`)); err != nil {
		t.Fatal(err)
	}
	call := (*calls)[1]
	if call.method != "POST" || call.path != "/devices/e1/applyConfigurationContent" {
		t.Errorf("apply request %+v", call)
	}
	if mc, ok := call.body["modulesContent"].(map[string]any); !ok || mc["$edgeAgent"] == nil {
		t.Errorf("apply body %v", call.body)
	}
}
