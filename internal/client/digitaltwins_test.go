package client

import (
	"context"
	"net/http"
	"net/http/httptest"
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
