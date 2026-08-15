package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestQuery_Paginates(t *testing.T) {
	var got []struct{ body, cont, max string }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = append(got, struct{ body, cont, max string }{string(b), r.Header.Get("x-ms-continuation"), r.Header.Get("x-ms-max-item-count")})
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-ms-item-type", "Raw")
		if r.Header.Get("x-ms-continuation") == "" {
			w.Header().Set("x-ms-continuation", "page2")
			_, _ = w.Write([]byte(`[{"deviceId":"a"},{"deviceId":"b"}]`))
			return
		}
		_, _ = w.Write([]byte(`[{"deviceId":"c"}]`))
	}))
	defer srv.Close()
	c, _ := newTestClient(t, srv, nil)
	items, itemType, err := c.Query(context.Background(), "SELECT deviceId FROM devices")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 || itemType != "Raw" || string(items[2]) != `{"deviceId":"c"}` {
		t.Errorf("items %v type %q", items, itemType)
	}
	if len(got) != 2 || got[0].max != "100" || got[1].cont != "page2" || got[0].cont != "" {
		t.Errorf("requests %+v", got)
	}
	var body map[string]string
	_ = json.Unmarshal([]byte(got[0].body), &body)
	if body["query"] != "SELECT deviceId FROM devices" {
		t.Errorf("body %s", got[0].body)
	}
}
