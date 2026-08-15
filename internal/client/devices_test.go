package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type recorded struct {
	method, path, rawQuery, ifMatch string
	body                            map[string]any
}

func deviceServer(t *testing.T, status int, respBody string) (*httptest.Server, *[]recorded) {
	t.Helper()
	var calls []recorded
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// RequestURI keeps the escaping the client sent (URL.Path is decoded).
		rec := recorded{method: r.Method, path: strings.SplitN(r.RequestURI, "?", 2)[0], rawQuery: r.URL.RawQuery, ifMatch: r.Header.Get("If-Match")}
		if b, _ := io.ReadAll(r.Body); len(b) > 0 {
			_ = json.Unmarshal(b, &rec.body)
		}
		calls = append(calls, rec)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(respBody))
	}))
	return srv, &calls
}

func TestDevices_CreateSendsNoPreconditionAndFullBody(t *testing.T) {
	srv, calls := deviceServer(t, 200, `{"deviceId":"d1","etag":"AAA","status":"enabled","authentication":{"type":"sas","symmetricKey":{"primaryKey":"k1","secondaryKey":"k2"}},"capabilities":{"iotEdge":false}}`)
	defer srv.Close()
	c, _ := newTestClient(t, srv, nil)
	d, err := c.CreateDevice(context.Background(), DeviceSpec{
		DeviceID: "d1", Status: StatusEnabled,
		Authentication: AuthenticationMechanism{Type: AuthTypeSAS},
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.ETag != "AAA" || d.Authentication.SymmetricKey.PrimaryKey != "k1" {
		t.Fatalf("decoded %+v", d)
	}
	call := (*calls)[0]
	if call.method != "PUT" || call.path != "/devices/d1" || call.ifMatch != "" {
		t.Fatalf("create must be PUT without If-Match: %+v", call)
	}
	// full-replace body: statusReason null, deviceScope "" for a leaf, no parentScopes
	if v, ok := call.body["statusReason"]; !ok || v != nil {
		t.Errorf("statusReason must be sent as null when empty, got %v (present %v)", v, ok)
	}
	if v, ok := call.body["deviceScope"]; !ok || v != "" {
		t.Errorf("leaf devices send deviceScope (\"\" clears): %v", call.body)
	}
	if _, ok := call.body["parentScopes"]; ok {
		t.Errorf("leaf devices must not send parentScopes: %v", call.body)
	}
	auth, ok := call.body["authentication"].(map[string]any)
	if !ok || auth["type"] != "sas" {
		t.Errorf("authentication missing: %v", call.body)
	}
	if _, ok := auth["symmetricKey"]; ok {
		t.Errorf("no keys given -> symmetricKey must be omitted so the hub generates keys: %v", call.body)
	}
}

func TestDevices_UpdateQuotesETag_EdgeScopes(t *testing.T) {
	srv, calls := deviceServer(t, 200, `{"deviceId":"e1","etag":"BBB"}`)
	defer srv.Close()
	c, _ := newTestClient(t, srv, nil)
	_, err := c.UpdateDevice(context.Background(), DeviceSpec{
		DeviceID: "e1", Status: StatusDisabled, StatusReason: "maintenance", IotEdge: true,
		ParentScope: "ms-azure-iot-edge://parent-1", OwnDeviceScope: "ms-azure-iot-edge://e1-42",
		Authentication: AuthenticationMechanism{Type: AuthTypeCertificateAuthority},
	}, "AAA")
	if err != nil {
		t.Fatal(err)
	}
	call := (*calls)[0]
	if call.ifMatch != `"AAA"` {
		t.Errorf("If-Match must be quoted, got %q", call.ifMatch)
	}
	if call.body["statusReason"] != "maintenance" || call.body["status"] != "disabled" {
		t.Errorf("body: %v", call.body)
	}
	if call.body["deviceScope"] != "ms-azure-iot-edge://e1-42" {
		t.Errorf("edge devices must echo their own deviceScope on update: %v", call.body)
	}
	if ps, ok := call.body["parentScopes"].([]any); !ok || len(ps) != 1 || ps[0] != "ms-azure-iot-edge://parent-1" {
		t.Errorf("edge devices send parentScopes: %v", call.body)
	}
	// edge without parent sends an explicit empty list; a leaf turning edge omits deviceScope
	_, _ = c.UpdateDevice(context.Background(), DeviceSpec{DeviceID: "e1", Status: StatusEnabled, IotEdge: true, Authentication: AuthenticationMechanism{Type: AuthTypeSAS}}, "*")
	call = (*calls)[1]
	if ps, ok := call.body["parentScopes"].([]any); !ok || len(ps) != 0 {
		t.Errorf("edge without parent must send parentScopes: []: %v", call.body)
	}
	if _, ok := call.body["deviceScope"]; ok {
		t.Errorf("without a current scope (create / leaf->edge) deviceScope must be omitted: %v", call.body)
	}
	if call.ifMatch != "*" {
		t.Errorf("If-Match * must pass through unquoted, got %q", call.ifMatch)
	}
}

func TestDevices_DeleteAndPathEscaping(t *testing.T) {
	srv, calls := deviceServer(t, 204, "")
	defer srv.Close()
	c, _ := newTestClient(t, srv, nil)
	if err := c.DeleteDevice(context.Background(), "dev#1?x%y", ""); err != nil {
		t.Fatal(err)
	}
	call := (*calls)[0]
	if call.method != "DELETE" || call.ifMatch != "*" {
		t.Errorf("delete: %+v", call)
	}
	if call.path != "/devices/dev%231%3Fx%25y" {
		t.Errorf("registry IDs must be path-escaped, got %q", call.path)
	}
	if call.rawQuery != "api-version="+APIVersion {
		t.Errorf("query = %q", call.rawQuery)
	}
}

func TestDevices_ErrorsAreTyped(t *testing.T) {
	srv, _ := deviceServer(t, 409, `{"Message":"ErrorCode:DeviceAlreadyExists;A device with ID 'd1' is already registered.","ExceptionMessage":"Tracking ID:x"}`)
	defer srv.Close()
	c, _ := newTestClient(t, srv, nil)
	_, err := c.CreateDevice(context.Background(), DeviceSpec{DeviceID: "d1", Status: StatusEnabled, Authentication: AuthenticationMechanism{Type: AuthTypeSAS}})
	if !IsConflict(err) {
		t.Fatalf("expected conflict, got %v", err)
	}
	e, _ := AsError(err)
	if e.Code != "DeviceAlreadyExists" || e.Message != "A device with ID 'd1' is already registered." {
		t.Errorf("parsed %+v", e)
	}
}

func TestQuoteETag(t *testing.T) {
	for in, want := range map[string]string{"": "", "*": "*", "AAA": `"AAA"`, `"AAA"`: `"AAA"`, " MQ== ": `"MQ=="`} {
		if got := QuoteETag(in); got != want {
			t.Errorf("QuoteETag(%q) = %q, want %q", in, got, want)
		}
	}
}
