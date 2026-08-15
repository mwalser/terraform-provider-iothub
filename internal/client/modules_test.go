package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestModules_CreateUpdateBody(t *testing.T) {
	srv, calls := deviceServer(t, 200, `{"deviceId":"d1","moduleId":"m1","etag":"AAA","managedBy":null,"authentication":{"type":"sas","symmetricKey":{"primaryKey":"k1","secondaryKey":"k2"}}}`)
	defer srv.Close()
	c, _ := newTestClient(t, srv, nil)
	m, err := c.CreateModule(context.Background(), ModuleSpec{DeviceID: "d1", ModuleID: "m1", Authentication: AuthenticationMechanism{Type: AuthTypeSAS}})
	if err != nil {
		t.Fatal(err)
	}
	if m.ModuleID != "m1" || m.ManagedBy != "" || m.Authentication.SymmetricKey.SecondaryKey != "k2" {
		t.Fatalf("decoded %+v", m)
	}
	call := (*calls)[0]
	if call.method != "PUT" || call.path != "/devices/d1/modules/m1" || call.ifMatch != "" {
		t.Fatalf("create must be PUT without If-Match: %+v", call)
	}
	if v, ok := call.body["managedBy"]; !ok || v != nil {
		t.Errorf("managedBy must be sent as null when empty (full replace), got %v (present %v)", v, ok)
	}
	if call.body["deviceId"] != "d1" || call.body["moduleId"] != "m1" {
		t.Errorf("body: %v", call.body)
	}

	_, err = c.UpdateModule(context.Background(), ModuleSpec{DeviceID: "d1", ModuleID: "m1", ManagedBy: "operator", Authentication: AuthenticationMechanism{Type: AuthTypeCertificateAuthority}}, "AAA")
	if err != nil {
		t.Fatal(err)
	}
	call = (*calls)[1]
	if call.ifMatch != `"AAA"` || call.body["managedBy"] != "operator" {
		t.Errorf("update: %+v", call)
	}
}

func TestModules_ListGetDelete(t *testing.T) {
	srv, calls := deviceServer(t, 200, `[{"deviceId":"d1","moduleId":"$edgeAgent","managedBy":"iotEdge"},{"deviceId":"d1","moduleId":"m1"}]`)
	defer srv.Close()
	c, _ := newTestClient(t, srv, nil)
	mods, err := c.ListModules(context.Background(), "d 1")
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) != 2 || mods[0].ModuleID != "$edgeAgent" || mods[0].ManagedBy != "iotEdge" {
		t.Errorf("list: %+v", mods)
	}
	if p := (*calls)[0].path; p != "/devices/d%201/modules" {
		t.Errorf("list path %q", p)
	}
	if err := c.DeleteModule(context.Background(), "d1", "$edgeHub", ""); err != nil {
		t.Fatal(err)
	}
	if call := (*calls)[1]; call.method != "DELETE" || call.ifMatch != "*" || call.path != "/devices/d1/modules/$edgeHub" {
		t.Errorf("delete: %+v", call)
	}
}

func TestModuleConnectionString(t *testing.T) {
	got := ModuleConnectionString("h.azure-devices.net", "d1", "m1", "k")
	if got != "HostName=h.azure-devices.net;DeviceId=d1;ModuleId=m1;SharedAccessKey=k" {
		t.Error(got)
	}
}

func TestModules_SAS401OnMissingDeviceBecomesNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/modules/") {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"Message":"{\"errorCode\":401002,\"message\":\"Unauthorized access\",\"trackingId\":\"t1\"}"}`))
			return
		}
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"Message":"ErrorCode:DeviceNotFound;x","ExceptionMessage":"Tracking ID:t2"}`))
	}))
	defer srv.Close()
	c, _ := newTestClient(t, srv, nil)
	_, err := c.GetModule(context.Background(), "gone", "m1")
	if !IsNotFound(err) || IsUnauthorized(err) {
		t.Fatalf("expected not-found, got %v", err)
	}
	if err := c.DeleteModule(context.Background(), "gone", "m1", ""); !IsNotFound(err) {
		t.Fatalf("delete: expected not-found, got %v", err)
	}

	// a real 401 (device readable) stays a 401
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/modules/") {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"Message":"ErrorCode:IotHubUnauthorizedAccess;Unauthorized"}`))
			return
		}
		_, _ = w.Write([]byte(`{"deviceId":"d1"}`))
	}))
	defer srv2.Close()
	c2, _ := newTestClient(t, srv2, nil)
	if _, err := c2.GetModule(context.Background(), "d1", "m1"); !IsUnauthorized(err) || IsModuleOnDisabledDevice(err) {
		t.Fatalf("expected 401 to pass through, got %v", err)
	}

	// a 401 on a module of a *disabled* device is explained (verified quirk)
	srv3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/modules") {
			w.Header().Set("iothub-errorcode", "IotHubUnauthorizedAccess")
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"Message":"{\"errorCode\":401002,\"message\":\"Unauthorized access\",\"trackingId\":\"t3\"}"}`))
			return
		}
		_, _ = w.Write([]byte(`{"deviceId":"d1","status":"disabled","etag":"E"}`))
	}))
	defer srv3.Close()
	c3, _ := newTestClient(t, srv3, nil)
	_, err = c3.ListModules(context.Background(), "d1")
	if !IsUnauthorized(err) || !IsModuleOnDisabledDevice(err) {
		t.Fatalf("expected the disabled-device explanation, got %v", err)
	}
	if e, _ := AsError(err); e.TrackingID != "t3" || !strings.Contains(e.Message, "disabled") {
		t.Errorf("error %+v", e)
	}
}
