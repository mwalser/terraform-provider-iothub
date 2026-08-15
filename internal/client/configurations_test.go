package client

import (
	"context"
	"encoding/json"
	"testing"
)

func TestConfigurations_CreateUpdateBody(t *testing.T) {
	srv, calls := deviceServer(t, 200, `{"id":"c1","etag":"MQ==","priority":10,"labels":{"a":"b"},"content":{"deviceContent":{"properties.desired.x":1}},"targetCondition":"*","systemMetrics":{"results":{"targetedCount":3},"queries":{"targetedCount":"select …"}},"metrics":{"results":{},"queries":{"m":"SELECT deviceId FROM devices"}}}`)
	defer srv.Close()
	c, _ := newTestClient(t, srv, nil)
	spec := ConfigurationSpec{
		ID: "c1", TargetCondition: "*", Priority: 10, Labels: map[string]string{"a": "b"},
		Content: ConfigurationContent{DeviceContent: json.RawMessage(`{"properties.desired.x":1}`)},
		Metrics: map[string]string{"m": "SELECT deviceId FROM devices"},
	}
	cfg, err := c.CreateConfiguration(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ETag != "MQ==" || cfg.SystemMetrics.Results["targetedCount"] != 3 || cfg.Metrics.Queries["m"] == "" || string(cfg.Content.DeviceContent) != `{"properties.desired.x":1}` {
		t.Errorf("decoded %+v", cfg)
	}
	call := (*calls)[0]
	if call.method != "PUT" || call.path != "/configurations/c1" || call.ifMatch != "" {
		t.Errorf("create: %+v", call)
	}
	if call.body["priority"] != float64(10) || call.body["targetCondition"] != "*" {
		t.Errorf("body: %v", call.body)
	}
	content, _ := call.body["content"].(map[string]any)
	if _, ok := content["deviceContent"]; !ok {
		t.Errorf("content must be sent: %v", call.body)
	}
	if _, ok := content["modulesContent"]; ok {
		t.Errorf("empty sections must be omitted: %v", call.body)
	}
	metrics, _ := call.body["metrics"].(map[string]any)
	if q, _ := metrics["queries"].(map[string]any); q["m"] != "SELECT deviceId FROM devices" {
		t.Errorf("metrics: %v", call.body)
	}
	if _, ok := call.body["schemaVersion"]; ok {
		t.Errorf("empty schemaVersion must be omitted: %v", call.body)
	}

	// update: quoted If-Match, content still present, empty labels sent as {}
	spec.Labels = map[string]string{}
	spec.Metrics = nil
	spec.SchemaVersion = "1.0"
	if _, err := c.UpdateConfiguration(context.Background(), spec, "MQ=="); err != nil {
		t.Fatal(err)
	}
	call = (*calls)[1]
	if call.ifMatch != `"MQ=="` {
		t.Errorf("If-Match must be quoted: %q", call.ifMatch)
	}
	if l, ok := call.body["labels"].(map[string]any); !ok || len(l) != 0 {
		t.Errorf("empty labels must be sent as {}: %v", call.body)
	}
	if _, ok := call.body["metrics"]; ok {
		t.Errorf("nil metrics must be omitted: %v", call.body)
	}
	if call.body["schemaVersion"] != "1.0" {
		t.Errorf("schemaVersion: %v", call.body)
	}
	if err := c.DeleteConfiguration(context.Background(), "c1", ""); err != nil {
		t.Fatal(err)
	}
	if call = (*calls)[2]; call.method != "DELETE" || call.ifMatch != "*" {
		t.Errorf("delete: %+v", call)
	}
}

func TestConfigurations_TestQueriesAndList(t *testing.T) {
	srv, calls := deviceServer(t, 200, `{"targetConditionError":"bad","customMetricQueryErrors":{"m":"worse"}}`)
	defer srv.Close()
	c, _ := newTestClient(t, srv, nil)
	res, err := c.TestConfigurationQueries(context.Background(), "tags.x=1", map[string]string{"m": "SELEC"})
	if err != nil {
		t.Fatal(err)
	}
	if res.OK() || res.TargetConditionError != "bad" || res.CustomMetricQueryErrors["m"] != "worse" {
		t.Errorf("result %+v", res)
	}
	call := (*calls)[0]
	if call.method != "POST" || call.path != "/configurations/testQueries" || call.body["targetCondition"] != "tags.x=1" {
		t.Errorf("request %+v", call)
	}
	if (&TestQueriesResult{}).OK() != true {
		t.Error("empty result is OK")
	}
	// without metrics the key is omitted
	_, _ = c.TestConfigurationQueries(context.Background(), "tags.x=1", nil)
	if _, ok := (*calls)[1].body["customMetricQueries"]; ok {
		t.Error("customMetricQueries must be omitted when empty")
	}

	srv2, calls2 := deviceServer(t, 200, `[{"id":"a"},{"id":"b"}]`)
	defer srv2.Close()
	c2, _ := newTestClient(t, srv2, nil)
	list, err := c2.ListConfigurations(context.Background())
	if err != nil || len(list) != 2 || (*calls2)[0].path != "/configurations" {
		t.Errorf("list: %v %v %+v", list, err, (*calls2)[0])
	}
}
