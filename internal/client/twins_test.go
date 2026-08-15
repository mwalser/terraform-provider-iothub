package client

import (
	"context"
	"testing"
)

func TestTwins_GetAndPatch(t *testing.T) {
	srv, calls := deviceServer(t, 200, `{"deviceId":"d1","moduleId":"m1","etag":"AAAAAAAAAAE=","deviceEtag":"NzU3","version":5,"tags":{"site":"munich","n":12345678901234567890},"properties":{"desired":{"$metadata":{},"$version":2,"a":1},"reported":{"$version":1}},"modelId":"","status":"enabled","x509Thumbprint":{"PrimaryThumbprint":"AB","SecondaryThumbprint":null},"capabilities":{"iotEdge":false}}`)
	defer srv.Close()
	c, _ := newTestClient(t, srv, nil)
	tw, err := c.GetModuleTwin(context.Background(), "d 1", "m1")
	if err != nil {
		t.Fatal(err)
	}
	if tw.Version != 5 || string(tw.Tags) != `{"site":"munich","n":12345678901234567890}` || string(tw.Properties.Desired) != `{"$metadata":{},"$version":2,"a":1}` {
		t.Errorf("decoded %+v", tw)
	}
	if tw.X509Thumbprint == nil || tw.X509Thumbprint.PrimaryThumbprint != "AB" {
		t.Errorf("capitalised thumbprint keys must decode: %+v", tw.X509Thumbprint)
	}
	if p := (*calls)[0].path; p != "/twins/d%201/modules/m1" {
		t.Errorf("path %q", p)
	}

	_, err = c.PatchDeviceTwin(context.Background(), "d1", TwinPatch{Tags: map[string]any{"site": nil}, Desired: map[string]any{"a": map[string]any{"b": 2}}}, "")
	if err != nil {
		t.Fatal(err)
	}
	call := (*calls)[1]
	if call.method != "PATCH" || call.path != "/twins/d1" || call.ifMatch != "*" {
		t.Errorf("patch: %+v", call)
	}
	if v, ok := call.body["tags"].(map[string]any); !ok || v["site"] != nil {
		t.Errorf("tags null must be sent: %v", call.body)
	}
	props, _ := call.body["properties"].(map[string]any)
	desired, _ := props["desired"].(map[string]any)
	a, _ := desired["a"].(map[string]any)
	if a["b"] != float64(2) {
		t.Errorf("desired: %v", call.body)
	}
	// only the sections given are sent
	_, _ = c.PatchDeviceTwin(context.Background(), "d1", TwinPatch{Tags: map[string]any{"x": 1}}, "AAAAAAAAAAE=")
	call = (*calls)[2]
	if _, ok := call.body["properties"]; ok || call.ifMatch != `"AAAAAAAAAAE="` {
		t.Errorf("tags-only patch: %+v", call)
	}
	if !(TwinPatch{}).IsEmpty() || (TwinPatch{Tags: map[string]any{}}).IsEmpty() {
		t.Error("IsEmpty")
	}
}
