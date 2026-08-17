package edgemanifest

import (
	"context"
	"math/big"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/mwalser/terraform-provider-iothub/internal/provider/configuration"
	"github.com/mwalser/terraform-provider-iothub/internal/twinpatch"
)

type obj = map[string]any

func num(f float64) *big.Float { return big.NewFloat(f) }

func agent(extra ...obj) obj {
	o := obj{"image": "mcr.microsoft.com/azureiotedge-agent:1.5"}
	for _, e := range extra {
		for k, v := range e {
			o[k] = v
		}
	}
	return o
}

func hub(extra ...obj) obj {
	o := obj{"image": "mcr.microsoft.com/azureiotedge-hub:1.5"}
	for _, e := range extra {
		for k, v := range e {
			o[k] = v
		}
	}
	return o
}

func mustBuild(t *testing.T, in any) string {
	t.Helper()
	out, problems := Build(in)
	if len(problems) > 0 {
		t.Fatalf("unexpected problems:\n%s", strings.Join(problems, "\n"))
	}
	return out
}

func decode(t *testing.T, s string) map[string]any {
	t.Helper()
	doc, err := twinpatch.Decode(s)
	if err != nil {
		t.Fatalf("output is not a JSON object: %v", err)
	}
	return doc
}

func TestBuild_minimal(t *testing.T) {
	out := mustBuild(t, obj{"edge_agent": agent(), "edge_hub": hub()})
	want := `{"$edgeAgent":{"properties.desired":{"modules":{},"runtime":{"settings":{"registryCredentials":{}},"type":"docker"},"schemaVersion":"1.1","systemModules":{"edgeAgent":{"settings":{"createOptions":"{}","image":"mcr.microsoft.com/azureiotedge-agent:1.5"},"type":"docker"},"edgeHub":{"restartPolicy":"always","settings":{"createOptions":"{\"HostConfig\":{\"PortBindings\":{\"443/tcp\":[{\"HostPort\":\"443\"}],\"5671/tcp\":[{\"HostPort\":\"5671\"}],\"8883/tcp\":[{\"HostPort\":\"8883\"}]}}}","image":"mcr.microsoft.com/azureiotedge-hub:1.5"},"status":"running","type":"docker"}}}},"$edgeHub":{"properties.desired":{"routes":{},"schemaVersion":"1.1","storeAndForwardConfiguration":{"timeToLiveSecs":7200}}}}`
	if out != want {
		t.Fatalf("minimal manifest:\n got %s\nwant %s", out, want)
	}
	if problems := configuration.ModulesContentType.Validate(decode(t, out)); len(problems) > 0 {
		t.Fatalf("output rejected by modules_content validation: %v", problems)
	}
}

func TestBuild_full(t *testing.T) {
	in := obj{
		"registry_credentials": obj{"acr": obj{"address": "contoso.azurecr.io", "username": "u", "password": "p"}},
		"edge_agent":           agent(obj{"env": obj{"RuntimeLogLevel": "debug"}, "image_pull_policy": "on-create"}),
		"edge_hub": hub(obj{
			"create_options": obj{"HostConfig": obj{"PortBindings": obj{"8883/tcp": []any{obj{"HostPort": "8883"}}}}},
			"startup_order":  num(0),
			"env":            obj{"OptimizeForPerformance": false},
		}),
		"modules": obj{
			"tempSensor": obj{
				"image":             "mcr.microsoft.com/azureiotedge-simulated-temperature-sensor:1.4",
				"create_options":    `{"HostConfig": {"Binds": ["/data:/data"]}, "Cmd": ["--fast"]}`,
				"env":               obj{"SendInterval": num(5), "MessageCount": num(2.5), "Verbose": true},
				"status":            "stopped",
				"restart_policy":    "on-unhealthy",
				"image_pull_policy": "never",
				"startup_order":     num(2),
				"version":           "1.0",
				"desired":           obj{"SendData": true, "SendInterval": num(5), "nested": obj{"a": []any{num(1), "x"}}},
			},
			"filter": obj{"image": "contoso.azurecr.io/filter:2.1.0"},
		},
		"routes": obj{
			"sensorToFilter": "FROM /messages/modules/tempSensor/outputs/temperatureOutput INTO BrokeredEndpoint(\"/modules/filter/inputs/input1\")",
			"upstream":       obj{"route": "FROM /messages/modules/filter/outputs/* WHERE $connectionModuleId = 'filter' INTO $upstream", "priority": num(1), "time_to_live_secs": num(86400)},
			"alerts":         obj{"route": "FROM /messages/* INTO $upstream", "priority": num(0)},
		},
		"store_and_forward": obj{"time_to_live_secs": num(3600), "max_size_bytes": num(1000000)},
	}
	out := mustBuild(t, in)
	want := obj{
		"$edgeAgent": obj{"properties.desired": obj{
			"schemaVersion": "1.1",
			"runtime": obj{"type": "docker", "settings": obj{"registryCredentials": obj{
				"acr": obj{"address": "contoso.azurecr.io", "username": "u", "password": "p"},
			}}},
			"systemModules": obj{
				"edgeAgent": obj{
					"type":            "docker",
					"settings":        obj{"image": "mcr.microsoft.com/azureiotedge-agent:1.5", "createOptions": "{}"},
					"env":             obj{"RuntimeLogLevel": obj{"value": "debug"}},
					"imagePullPolicy": "on-create",
				},
				"edgeHub": obj{
					"type": "docker", "status": "running", "restartPolicy": "always",
					"settings":     obj{"image": "mcr.microsoft.com/azureiotedge-hub:1.5", "createOptions": `{"HostConfig":{"PortBindings":{"8883/tcp":[{"HostPort":"8883"}]}}}`},
					"env":          obj{"OptimizeForPerformance": obj{"value": false}},
					"startupOrder": 0,
				},
			},
			"modules": obj{
				"tempSensor": obj{
					"type": "docker", "status": "stopped", "restartPolicy": "on-unhealthy",
					"settings":        obj{"image": "mcr.microsoft.com/azureiotedge-simulated-temperature-sensor:1.4", "createOptions": `{"Cmd":["--fast"],"HostConfig":{"Binds":["/data:/data"]}}`},
					"env":             obj{"SendInterval": obj{"value": 5}, "MessageCount": obj{"value": 2.5}, "Verbose": obj{"value": true}},
					"imagePullPolicy": "never",
					"startupOrder":    2,
					"version":         "1.0",
				},
				"filter": obj{
					"type": "docker", "status": "running", "restartPolicy": "always",
					"settings": obj{"image": "contoso.azurecr.io/filter:2.1.0", "createOptions": "{}"},
				},
			},
		}},
		"$edgeHub": obj{"properties.desired": obj{
			"schemaVersion": "1.1",
			"routes": obj{
				"sensorToFilter": "FROM /messages/modules/tempSensor/outputs/temperatureOutput INTO BrokeredEndpoint(\"/modules/filter/inputs/input1\")",
				"upstream":       obj{"route": "FROM /messages/modules/filter/outputs/* WHERE $connectionModuleId = 'filter' INTO $upstream", "priority": 1, "timeToLiveSecs": 86400},
				"alerts":         obj{"route": "FROM /messages/* INTO $upstream", "priority": 0},
			},
			"storeAndForwardConfiguration": obj{"timeToLiveSecs": 3600, "storeLimits": obj{"maxSizeBytes": 1000000}},
		}},
		"tempSensor": obj{"properties.desired": obj{"SendData": true, "SendInterval": 5, "nested": obj{"a": []any{1, "x"}}}},
	}
	if got, exp := decode(t, out), decode(t, twinpatch.Encode(want)); !twinpatch.Equal(got, exp) {
		t.Fatalf("full manifest:\n got %s\nwant %s", out, twinpatch.Encode(want))
	}
	if out != twinpatch.Encode(decode(t, out)) {
		t.Fatalf("output is not in canonical form: %s", out)
	}
	if problems := configuration.ModulesContentType.Validate(decode(t, out)); len(problems) > 0 {
		t.Fatalf("output rejected by modules_content validation: %v", problems)
	}
}

func TestBuild_layered(t *testing.T) {
	out := mustBuild(t, obj{
		"layered": true,
		"modules": obj{"tempSensor": obj{"image": "mcr.microsoft.com/azureiotedge-simulated-temperature-sensor:1.4", "desired": obj{"SendInterval": num(10)}}},
		"routes":  obj{"sensor": "FROM /messages/modules/tempSensor/* INTO $upstream"},
	})
	want := obj{
		"$edgeAgent": obj{"properties.desired.modules.tempSensor": obj{
			"type": "docker", "status": "running", "restartPolicy": "always",
			"settings": obj{"image": "mcr.microsoft.com/azureiotedge-simulated-temperature-sensor:1.4", "createOptions": "{}"},
		}},
		"$edgeHub":   obj{"properties.desired.routes.sensor": "FROM /messages/modules/tempSensor/* INTO $upstream"},
		"tempSensor": obj{"properties.desired": obj{"SendInterval": 10}},
	}
	if got, exp := decode(t, out), decode(t, twinpatch.Encode(want)); !twinpatch.Equal(got, exp) {
		t.Fatalf("layered manifest:\n got %s\nwant %s", out, twinpatch.Encode(want))
	}
	// Without routes there is no $edgeHub; without modules $edgeAgent is empty.
	out = mustBuild(t, obj{"layered": true, "modules": obj{"m": obj{"image": "x"}}})
	if doc := decode(t, out); doc["$edgeHub"] != nil {
		t.Errorf("layered manifest without routes must not carry $edgeHub: %s", out)
	}
	out = mustBuild(t, obj{"layered": true, "routes": obj{"r": "FROM /* INTO $upstream"}})
	if out != `{"$edgeAgent":{},"$edgeHub":{"properties.desired.routes.r":"FROM /* INTO $upstream"}}` {
		t.Errorf("layered manifest without modules: %s", out)
	}
	if problems := configuration.ModulesContentType.Validate(decode(t, out)); len(problems) > 0 {
		t.Fatalf("output rejected by modules_content validation: %v", problems)
	}
}

func TestBuild_createOptionsChunks(t *testing.T) {
	long := strings.Repeat("x", 1200)
	out := mustBuild(t, obj{
		"edge_agent": agent(), "edge_hub": hub(),
		"modules": obj{"m": obj{"image": "img", "create_options": obj{"Env": []any{long}}}},
	})
	doc := decode(t, out)
	settings, _ := twinpatch.Get(doc, twinpatch.Path{"$edgeAgent", "properties.desired", "modules", "m", "settings"})
	s, ok := settings.(map[string]any)
	if !ok {
		t.Fatalf("no settings in %s", out)
	}
	whole := `{"Env":["` + long + `"]}`
	var joined string
	for _, key := range []string{"createOptions", "createOptions01", "createOptions02"} {
		chunk, ok := s[key].(string)
		if !ok {
			t.Fatalf("missing %s in %v", key, s)
		}
		if len(chunk) > createOptionsChunk {
			t.Errorf("%s has %d characters", key, len(chunk))
		}
		joined += chunk
	}
	if _, ok := s["createOptions03"]; ok {
		t.Errorf("unexpected fourth chunk")
	}
	if joined != whole {
		t.Errorf("chunks do not join to the original:\n%s\n%s", joined, whole)
	}
	// Exactly 512 characters stays one chunk.
	exact := strings.Repeat("y", createOptionsChunk-len(`{"Env":[""]}`))
	out = mustBuild(t, obj{"edge_agent": agent(), "edge_hub": hub(), "modules": obj{"m": obj{"image": "img", "create_options": `{"Env":["` + exact + `"]}`}}})
	if strings.Contains(out, "createOptions01") {
		t.Errorf("512 characters must not be chunked: %s", out)
	}
	// Beyond 100 chunks is an error.
	_, problems := Build(obj{"edge_agent": agent(), "edge_hub": hub(), "modules": obj{"m": obj{"image": "img", "create_options": obj{"Env": []any{strings.Repeat("z", 60000)}}}}})
	if len(problems) != 1 || !strings.Contains(problems[0], `modules["m"].create_options: is 60012 characters; the limit is 51200`) {
		t.Errorf("oversized createOptions: %v", problems)
	}
}

func TestBuild_createOptionsForms(t *testing.T) {
	asObject := mustBuild(t, obj{"edge_agent": agent(), "edge_hub": hub(), "modules": obj{"m": obj{"image": "img", "create_options": obj{"HostConfig": obj{"Privileged": true, "Memory": num(1048576)}, "Cmd": []any{"a"}}}}})
	asString := mustBuild(t, obj{"edge_agent": agent(), "edge_hub": hub(), "modules": obj{"m": obj{"image": "img", "create_options": ` { "Cmd" : ["a"], "HostConfig": {"Memory": 1048576, "Privileged": true} } `}}})
	if asObject != asString {
		t.Fatalf("object and string create_options differ:\n%s\n%s", asObject, asString)
	}
	if !strings.Contains(asObject, `\"Memory\":1048576`) {
		t.Errorf("number formatting: %s", asObject)
	}
}

func TestBuild_problems(t *testing.T) {
	base := func(extra obj) obj {
		o := obj{"edge_agent": agent(), "edge_hub": hub()}
		for k, v := range extra {
			o[k] = v
		}
		return o
	}
	cases := []struct {
		name string
		in   any
		want []string // substrings, one per expected problem, in order
	}{
		{"not an object", "x", []string{"must be an object, got a string"}},
		{"images required", obj{}, []string{"edge_agent: required", "edge_hub: required"}},
		{"image missing", obj{"edge_agent": obj{}, "edge_hub": hub()}, []string{"edge_agent.image: required"}},
		{"image null", obj{"edge_agent": obj{"image": nil}, "edge_hub": hub()}, []string{"edge_agent.image: required"}},
		{"image blank", obj{"edge_agent": obj{"image": " "}, "edge_hub": hub()}, []string{"edge_agent.image: must not be empty"}},
		{"image invalid", obj{"edge_agent": obj{"image": "a:b:c"}, "edge_hub": hub()}, []string{"edge_agent.image: not a container image reference"}},
		{"image whitespace", obj{"edge_agent": obj{"image": "repo name:tag"}, "edge_hub": hub()}, []string{"edge_agent.image: not a container image reference"}},
		{"image unicode whitespace", obj{"edge_agent": obj{"image": "repo\u00a0name:tag"}, "edge_hub": hub()}, []string{"edge_agent.image: not a container image reference"}},
		{"unknown key", base(obj{"module": obj{}}), []string{"module: unknown key; accepted: edge_agent, edge_hub, layered, modules, registry_credentials, routes, store_and_forward"}},
		{"unknown module key", base(obj{"modules": obj{"m": obj{"image": "i", "restartPolicy": "always"}}}), []string{`modules["m"].restartPolicy: unknown key; accepted: create_options, desired, env, image, image_pull_policy, restart_policy, startup_order, status, version`}},
		{"unknown agent key", base(obj{"edge_agent": agent(obj{"startup_order": num(1)})}), []string{"edge_agent.startup_order: unknown key; accepted: create_options, env, image, image_pull_policy"}},
		{"module name", base(obj{"modules": obj{"temp sensor": obj{"image": "i"}}}), []string{`modules["temp sensor"]: module names may only contain`}},
		{"system module name", base(obj{"modules": obj{"edgeHub": obj{"image": "i"}}}), []string{`modules["edgeHub"]: "edgeHub" is a system module`}},
		{"enum", base(obj{"modules": obj{"m": obj{"image": "i", "status": "Running"}}}), []string{`modules["m"].status: must be one of "running", "stopped", got "Running"`}},
		{"restart policy type", base(obj{"modules": obj{"m": obj{"image": "i", "restart_policy": true}}}), []string{`modules["m"].restart_policy: must be a string, got a bool`}},
		{"startup order fraction", base(obj{"modules": obj{"m": obj{"image": "i", "startup_order": num(1.5)}}}), []string{`modules["m"].startup_order: must be a whole number between 0 and 4294967295, got 1.5`}},
		{"startup order range", base(obj{"modules": obj{"m": obj{"image": "i", "startup_order": num(-1)}}}), []string{`modules["m"].startup_order: must be between 0 and 4294967295, got -1`}},
		{"env name", base(obj{"modules": obj{"m": obj{"image": "i", "env": obj{"a.b": "x"}}}}), []string{`modules["m"].env["a.b"]: environment variable names must not contain`}},
		{"env value", base(obj{"modules": obj{"m": obj{"image": "i", "env": obj{"A": obj{}}}}}), []string{`modules["m"].env["A"]: must be a string, number or bool, got an object`}},
		{"env null", base(obj{"modules": obj{"m": obj{"image": "i", "env": obj{"A": nil}}}}), []string{`modules["m"].env["A"]: must be a string, number or bool, got null`}},
		{"env not a map", base(obj{"modules": obj{"m": obj{"image": "i", "env": []any{"A=1"}}}}), []string{`modules["m"].env: must be a map, got a list`}},
		{"create options type", base(obj{"modules": obj{"m": obj{"image": "i", "create_options": num(1)}}}), []string{`modules["m"].create_options: must be an object or a JSON string, got a number`}},
		{"create options string", base(obj{"modules": obj{"m": obj{"image": "i", "create_options": "[1]"}}}), []string{`modules["m"].create_options: expected a JSON object`}},
		{"desired null", base(obj{"modules": obj{"m": obj{"image": "i", "desired": obj{"a": nil}}}}), []string{`modules["m"].desired: a: null is not a twin value`}},
		{"desired key", base(obj{"modules": obj{"m": obj{"image": "i", "desired": obj{"a.b": num(1)}}}}), []string{`modules["m"].desired: a.b: key must not contain`}},
		{"credential fields", base(obj{"registry_credentials": obj{"acr": obj{"address": "x", "password": ""}}}), []string{`registry_credentials["acr"].username: required`, `registry_credentials["acr"].password: must not be empty`}},
		{"credential label", base(obj{"registry_credentials": obj{"my acr": obj{"address": "x", "username": "u", "password": "p"}}}), []string{`registry_credentials["my acr"]: key must not contain`}},
		{"credential address whitespace", base(obj{"registry_credentials": obj{"acr": obj{"address": "contoso .azurecr.io", "username": "u", "password": "p"}}}), []string{`registry_credentials["acr"].address: must not contain whitespace`}},
		{"route shape", base(obj{"routes": obj{"r": "SELECT deviceId FROM devices"}}), []string{`routes["r"]: must have the form FROM <source>`}},
		{"route sink", base(obj{"routes": obj{"r": "FROM /messages/* INTO $downstream"}}), []string{`routes["r"]: must have the form`}},
		{"route endpoint", base(obj{"routes": obj{"r": obj{"route": `FROM /messages/* INTO BrokeredEndpoint("/modules/filter/input1")`}}}), []string{`routes["r"].route: the BrokeredEndpoint must be "/modules/<module>/inputs/<input>", got "/modules/filter/input1"`}},
		{"route priority", base(obj{"routes": obj{"r": obj{"route": "FROM /* INTO $upstream", "priority": num(10)}}}), []string{`routes["r"].priority: must be between 0 and 9, got 10`}},
		{"route missing", base(obj{"routes": obj{"r": obj{"priority": num(1)}}}), []string{`routes["r"].route: required`}},
		{"route type", base(obj{"routes": obj{"r": num(1)}}), []string{`routes["r"]: must be a route string or an object`}},
		{"route name", base(obj{"routes": obj{"a.b": "FROM /* INTO $upstream"}}), []string{`routes["a.b"]: key must not contain`}},
		{"ttl", base(obj{"store_and_forward": obj{"time_to_live_secs": num(-2)}}), []string{"store_and_forward.time_to_live_secs: must be between -1 and 2147483647, got -2"}},
		{"max size", base(obj{"store_and_forward": obj{"max_size_bytes": num(0)}}), []string{"store_and_forward.max_size_bytes: must be between 1 and 9223372036854775807, got 0"}},
		{"layered extras", obj{"layered": true, "edge_agent": agent(), "store_and_forward": obj{}}, []string{"edge_agent: not allowed in a layered manifest", "store_and_forward: not allowed in a layered manifest"}},
		{"layered type", obj{"layered": "yes", "edge_agent": agent(), "edge_hub": hub()}, []string{"layered: must be true or false, got a string"}},
		{"long env value", base(obj{"modules": obj{"m": obj{"image": "i", "env": obj{"A": strings.Repeat("v", 5000)}}}}), []string{"$edgeAgent.properties.desired: modules.m.env.A.value: string value is 5000 bytes, the maximum is 4096"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, problems := Build(tc.in)
			if len(problems) != len(tc.want) {
				t.Fatalf("got %d problems, want %d:\n%s", len(problems), len(tc.want), strings.Join(problems, "\n"))
			}
			for i, w := range tc.want {
				if !strings.Contains(problems[i], w) {
					t.Errorf("problem %d = %q, want it to contain %q", i, problems[i], w)
				}
			}
		})
	}
}

func TestBuild_routeSkeleton(t *testing.T) {
	ok := []string{
		"FROM /* INTO $upstream",
		"from /messages/* into $UPSTREAM",
		"FROM /messages/modules/* INTO $upstream",
		"FROM /messages/modules/m/* INTO $upstream",
		"FROM /messages/modules/m/outputs/* INTO $upstream",
		"FROM /messages/modules/m/outputs/o INTO $upstream",
		"SELECT * FROM /messages/modules/m/outputs/o WHERE $connectionDeviceId = 'd' AND temp > 30 INTO $upstream",
		`FROM /messages/* WHERE $body.into = 1 INTO BrokeredEndpoint("/modules/f/inputs/i")`,
		"FROM /twinChangeNotifications INTO $upstream",
		"FROM /messages/* WHERE NOT IS_DEFINED($body.x)\n  INTO $upstream",
	}
	for _, r := range ok {
		var p problems
		checkRoute(&p, "r", r)
		if len(p) > 0 {
			t.Errorf("%q rejected: %v", r, p)
		}
	}
	bad := []string{"", "INTO $upstream", "FROM messages INTO $upstream", "FROM /* INTO upstream", `FROM /* INTO BrokeredEndpoint(/modules/a/inputs/b)`, "FROM /* WHERE x INTO"}
	for _, r := range bad {
		var p problems
		checkRoute(&p, "r", r)
		if len(p) == 0 {
			t.Errorf("%q accepted", r)
		}
	}
}

func TestToGo(t *testing.T) {
	v := tftypes.NewValue(tftypes.Object{AttributeTypes: map[string]tftypes.Type{
		"s": tftypes.String, "n": tftypes.Number, "b": tftypes.Bool, "nul": tftypes.String,
		"l": tftypes.Tuple{ElementTypes: []tftypes.Type{tftypes.Number, tftypes.String}},
		"m": tftypes.Map{ElementType: tftypes.String},
	}}, map[string]tftypes.Value{
		"s":   tftypes.NewValue(tftypes.String, "x"),
		"n":   tftypes.NewValue(tftypes.Number, big.NewFloat(2.5)),
		"b":   tftypes.NewValue(tftypes.Bool, true),
		"nul": tftypes.NewValue(tftypes.String, nil),
		"l":   tftypes.NewValue(tftypes.Tuple{ElementTypes: []tftypes.Type{tftypes.Number, tftypes.String}}, []tftypes.Value{tftypes.NewValue(tftypes.Number, big.NewFloat(1)), tftypes.NewValue(tftypes.String, "y")}),
		"m":   tftypes.NewValue(tftypes.Map{ElementType: tftypes.String}, map[string]tftypes.Value{"k": tftypes.NewValue(tftypes.String, "v")}),
	})
	got, err := toGo(v)
	if err != nil {
		t.Fatal(err)
	}
	want := obj{"s": "x", "n": 2.5, "b": true, "nul": nil, "l": []any{1, "y"}, "m": obj{"k": "v"}}
	if !twinpatch.Equal(decode(t, twinpatch.Encode(toJSONMap(got.(map[string]any)))), decode(t, twinpatch.Encode(want))) { //nolint:forcetypeassert // an object was passed in
		t.Fatalf("toGo = %#v", got)
	}
	unknown := tftypes.NewValue(tftypes.String, tftypes.UnknownValue)
	if _, err := toGo(unknown); err == nil {
		t.Error("unknown value must be an error")
	}
}

func TestFunction_Run(t *testing.T) {
	ctx := context.Background()
	f := New()
	var def function.DefinitionResponse
	f.Definition(ctx, function.DefinitionRequest{}, &def)
	var valid function.DefinitionValidateResponse
	def.Definition.ValidateImplementation(ctx, function.DefinitionValidateRequest{FuncName: "edge_manifest"}, &valid)
	if valid.Diagnostics.HasError() {
		t.Fatalf("definition: %v", valid.Diagnostics)
	}
	imageT := map[string]attr.Type{"image": types.StringType}
	arg := types.ObjectValueMust(map[string]attr.Type{
		"edge_agent": types.ObjectType{AttrTypes: imageT},
		"edge_hub":   types.ObjectType{AttrTypes: imageT},
		"routes":     types.MapType{ElemType: types.StringType},
	}, map[string]attr.Value{
		"edge_agent": types.ObjectValueMust(imageT, map[string]attr.Value{"image": types.StringValue("mcr.microsoft.com/azureiotedge-agent:1.5")}),
		"edge_hub":   types.ObjectValueMust(imageT, map[string]attr.Value{"image": types.StringValue("mcr.microsoft.com/azureiotedge-hub:1.5")}),
		"routes":     types.MapValueMust(types.StringType, map[string]attr.Value{"all": types.StringValue("FROM /* INTO $upstream")}),
	})
	run := func(v attr.Value) function.RunResponse {
		resp := function.RunResponse{Result: function.NewResultData(types.StringUnknown())}
		f.Run(ctx, function.RunRequest{Arguments: function.NewArgumentsData([]attr.Value{types.DynamicValue(v)})}, &resp)
		return resp
	}
	resp := run(arg)
	if resp.Error != nil {
		t.Fatalf("run: %v", resp.Error)
	}
	out, ok := resp.Result.Value().(types.String)
	if !ok {
		t.Fatalf("result is %T", resp.Result.Value())
	}
	if !strings.Contains(out.ValueString(), `"routes":{"all":"FROM /* INTO $upstream"}`) {
		t.Errorf("result: %s", out.ValueString())
	}

	bad := types.ObjectValueMust(map[string]attr.Type{"edge_agent": types.ObjectType{AttrTypes: imageT}, "typo": types.BoolType}, map[string]attr.Value{
		"edge_agent": types.ObjectValueMust(imageT, map[string]attr.Value{"image": types.StringValue("")}),
		"typo":       types.BoolValue(true),
	})
	resp = run(bad)
	if resp.Error == nil {
		t.Fatal("expected an error")
	}
	if want := "3 problems:\n  - edge_agent.image: must not be empty\n  - edge_hub: required (the image of the IoT Edge hub)\n  - typo: unknown key; accepted: edge_agent, edge_hub, layered, modules, registry_credentials, routes, store_and_forward"; !strings.Contains(resp.Error.Error(), want) {
		t.Errorf("error = %q, want %q", resp.Error.Error(), want)
	}
	if resp.Error.FunctionArgument == nil || *resp.Error.FunctionArgument != 0 {
		t.Errorf("error is not attributed to the manifest argument: %+v", resp.Error)
	}
}
