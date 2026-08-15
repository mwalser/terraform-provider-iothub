package client

import (
	"context"
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
