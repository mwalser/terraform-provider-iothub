package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestDigitalTwins_Get(t *testing.T) {
	doc := `{"$dtId":"d1","$metadata":{"$model":"dtmi:com:example:Thermostat;1","serialNumber":{"lastUpdateTime":"2026-08-15T19:32:21Z"}},"serialNumber":"SN-1","thermostat1":{"$metadata":{},"maxTemp":42.5}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/digitaltwins/d1" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("ETag", `"AAAAAAAAAAI="`)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(doc))
	}))
	defer srv.Close()
	c, _ := newTestClient(t, srv, nil)
	dt, err := c.GetDigitalTwin(context.Background(), "d1")
	if err != nil {
		t.Fatal(err)
	}
	if dt.ID != "d1" || dt.ModelID != "dtmi:com:example:Thermostat;1" || dt.ETag != "AAAAAAAAAAI=" || string(dt.Document) != doc {
		t.Errorf("digital twin %+v", dt)
	}

	// non-PnP device: empty model, document still returned
	srv2, _ := deviceServer(t, 200, `{"$dtId":"d2","$metadata":{"$model":""}}`)
	defer srv2.Close()
	c2, _ := newTestClient(t, srv2, nil)
	dt, err = c2.GetDigitalTwin(context.Background(), "d2")
	if err != nil || dt.ModelID != "" || dt.ID != "d2" {
		t.Errorf("non-PnP: %+v %v", dt, err)
	}

	srv3, _ := deviceServer(t, 404, `{"Message":"ErrorCode:DeviceNotFound;d3","ExceptionMessage":"Tracking ID:x"}`)
	defer srv3.Close()
	c3, _ := newTestClient(t, srv3, nil)
	if _, err := c3.GetDigitalTwin(context.Background(), "d3"); !IsNotFound(err) {
		t.Errorf("expected not found, got %v", err)
	}
}

func TestDigitalTwins_InvokeCommand(t *testing.T) {
	var got struct {
		method, path, query string
		body                []byte
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method, got.path, got.query = r.Method, r.URL.Path, r.URL.RawQuery
		got.body, _ = io.ReadAll(r.Body)
		w.Header().Set("x-ms-command-statuscode", "201")
		w.Header().Set("x-ms-request-id", "req-1")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	c, _ := newTestClient(t, srv, nil)

	res, err := c.InvokeDigitalTwinCommand(context.Background(), DigitalTwinCommand{
		DigitalTwinID: "d1", CommandName: "reboot", Payload: json.RawMessage(`{"delay":1}`), ResponseTimeoutSeconds: 30, ConnectTimeoutSeconds: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != 201 || string(res.Payload) != `{"ok":true}` || res.RequestID != "req-1" {
		t.Errorf("result %+v", res)
	}
	if got.method != "POST" || got.path != "/digitaltwins/d1/commands/reboot" || string(got.body) != `{"delay":1}` {
		t.Errorf("request %+v", got)
	}
	if got.query != "api-version=2021-04-12&connectTimeoutInSeconds=5&responseTimeoutInSeconds=30" {
		t.Errorf("query %q", got.query)
	}

	// component command, nil payload → null
	if _, err := c.InvokeDigitalTwinCommand(context.Background(), DigitalTwinCommand{
		DigitalTwinID: "d1", ComponentPath: "thermostat1", CommandName: "getMaxMinReport", ResponseTimeoutSeconds: 10,
	}); err != nil {
		t.Fatal(err)
	}
	if got.path != "/digitaltwins/d1/components/thermostat1/commands/getMaxMinReport" || string(got.body) != "null" {
		t.Errorf("component request %+v", got)
	}

	// offline device (nested envelope 2 with 404103) and Entra ID rejection (401002)
	srv2, _ := deviceServer(t, 404, `{"Message":"{\"errorCode\":404103,\"message\":\"The operation failed because the requested device isn't online.\",\"trackingId\":\"t\"}","ExceptionMessage":""}`)
	defer srv2.Close()
	c2, _ := newTestClient(t, srv2, nil)
	if _, err := c2.InvokeDigitalTwinCommand(context.Background(), DigitalTwinCommand{DigitalTwinID: "d1", CommandName: "x", ResponseTimeoutSeconds: 5}); !IsDeviceNotOnline(err) {
		t.Errorf("expected DeviceNotOnline, got %v", err)
	}
	srv3, _ := deviceServer(t, 401, `{"Message":"{\"errorCode\":401002,\"message\":\"Unauthorized access\",\"trackingId\":\"t\"}","ExceptionMessage":""}`)
	defer srv3.Close()
	c3, _ := newTestClient(t, srv3, nil)
	if _, err := c3.InvokeDigitalTwinCommand(context.Background(), DigitalTwinCommand{DigitalTwinID: "d1", CommandName: "x", ResponseTimeoutSeconds: 5}); !IsUnauthorized(err) {
		t.Errorf("expected unauthorized, got %v", err)
	}
}

func TestDigitalTwins_CommandNotRetriedOn5xx(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&n, 1) == 1 {
			w.WriteHeader(500)
			_, _ = w.Write([]byte(`{"Message":"ErrorCode:ServerError;boom"}`))
			return
		}
		w.Header().Set("x-ms-command-statuscode", "200")
	}))
	defer srv.Close()
	c, _ := newTestClient(t, srv, nil)
	if _, err := c.InvokeDigitalTwinCommand(context.Background(), DigitalTwinCommand{DigitalTwinID: "d1", CommandName: "x", ResponseTimeoutSeconds: 5}); err == nil {
		t.Fatal("expected the 500 to surface without retry")
	}
	if atomic.LoadInt32(&n) != 1 {
		t.Errorf("command was re-sent %d times", n-1)
	}
	// empty body → nil payload, status from header
	res, err := c.InvokeDigitalTwinCommand(context.Background(), DigitalTwinCommand{DigitalTwinID: "d1", CommandName: "x", ResponseTimeoutSeconds: 5})
	if err != nil || res.Status != 200 || res.Payload != nil {
		t.Errorf("second call: %+v %v", res, err)
	}
}
