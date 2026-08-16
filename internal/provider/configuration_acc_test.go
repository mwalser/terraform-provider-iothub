package provider_test

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	iotacc "github.com/mwalser/terraform-provider-iothub/internal/acctest"
	"github.com/mwalser/terraform-provider-iothub/internal/client"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/jsondoc"
)

func checkConfigurationDestroyed(ids ...string) func(*terraform.State) error {
	return func(_ *terraform.State) error {
		c, err := iotacc.NewClient()
		if err != nil {
			return err
		}
		for _, id := range ids {
			if _, err := c.GetConfiguration(context.Background(), id); err == nil {
				return fmt.Errorf("configuration %q still exists after destroy", id)
			} else if !client.IsNotFound(err) {
				return err
			}
		}
		return nil
	}
}

// sideEffect is a plan check that runs an out-of-band change between plan
// and apply — the window the If-Match check protects.
type sideEffect func()

func (f sideEffect) CheckPlan(_ context.Context, _ plancheck.CheckPlanRequest, _ *plancheck.CheckPlanResponse) {
	f()
}

func TestAccConfiguration_basic(t *testing.T) {
	id := acctest.RandomWithPrefix("tf-acc")
	res := "iothub_configuration.test"
	cfg := func(target string, priority int, labels, metrics, content string) string {
		return iotacc.ProviderConfig() + fmt.Sprintf(`
resource "iothub_configuration" "test" {
  configuration_id = %q
  target_condition = %q
  priority         = %d
  labels           = %s
  metrics          = %s
  device_content   = %s
}`, id, target, priority, labels, metrics, content)
	}
	content1 := `jsonencode({ "properties.desired.firmware" = { channel = "stable", build = 7 } })`
	metrics1 := `{ applied = "SELECT deviceId FROM devices WHERE properties.reported.firmware.channel = 'stable'" }`
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { iotacc.PreCheck(t) },
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactories,
		CheckDestroy:             checkConfigurationDestroyed(id),
		Steps: []resource.TestStep{
			{
				Config: cfg("tags.fleet.region = 'eu'", 10, `{ owner = "platform" }`, metrics1, content1),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("id"), knownvalue.StringExact(id)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("priority"), knownvalue.Int64Exact(10)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("labels").AtMapKey("owner"), knownvalue.StringExact("platform")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("metrics").AtMapKey("applied"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("device_content"), knownvalue.StringExact(`{"properties.desired.firmware":{"build":7,"channel":"stable"}}`)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("module_content"), knownvalue.Null()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("schema_version"), knownvalue.Null()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("etag"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("system_metrics"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("created_time"), knownvalue.NotNull()),
				},
			},
			{
				Config:           cfg("tags.fleet.region = 'eu'", 10, `{ owner = "platform" }`, metrics1, content1),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
			{ // in-place update of everything mutable; content untouched
				Config:           cfg("tags.fleet.region = 'eu' AND tags.ring = 2", 20, `{ owner = "ops", stage = "canary" }`, `{}`, content1),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)}},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("priority"), knownvalue.Int64Exact(20)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("target_condition"), knownvalue.StringExact("tags.fleet.region = 'eu' AND tags.ring = 2")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("labels"), knownvalue.MapSizeExact(2)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("metrics"), knownvalue.MapSizeExact(0)),
				},
			},
			{ // reformatted content (semantically equal): an update, not a replacement, and the hub still holds the same content
				Config: cfg("tags.fleet.region = 'eu' AND tags.ring = 2", 20, `{ owner = "ops", stage = "canary" }`, `{}`,
					`"{\"properties.desired.firmware\": {\"channel\": \"stable\", \"build\": 7.0}}"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)}},
				Check: func(_ *terraform.State) error {
					c, err := iotacc.Client(t).GetConfiguration(context.Background(), id)
					if err != nil {
						return err
					}
					if !jsondoc.SemanticallyEqual(string(c.Content.DeviceContent), `{"properties.desired.firmware":{"channel":"stable","build":7}}`) {
						return fmt.Errorf("content changed on the hub: %s", c.Content.DeviceContent)
					}
					return nil
				},
			},
			{ // content change: replacement (the hub would silently keep the old content on an update)
				Config:           cfg("tags.fleet.region = 'eu' AND tags.ring = 2", 20, `{ owner = "ops", stage = "canary" }`, `{}`, `jsonencode({ "properties.desired.firmware" = { channel = "beta" } })`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionReplace)}},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("device_content"), knownvalue.StringExact(`{"properties.desired.firmware":{"channel":"beta"}}`)),
				},
				Check: func(_ *terraform.State) error {
					c, err := iotacc.Client(t).GetConfiguration(context.Background(), id)
					if err != nil {
						return err
					}
					if !jsondoc.SemanticallyEqual(string(c.Content.DeviceContent), `{"properties.desired.firmware":{"channel":"beta"}}`) {
						return fmt.Errorf("content after replacement: %s", c.Content.DeviceContent)
					}
					return nil
				},
			},
			{
				ResourceName:            res,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"timeouts", "system_metrics", "metric_results", "last_updated_time"},
			},
		},
	})
}

func TestAccConfiguration_conflictsAndDisappears(t *testing.T) {
	id := acctest.RandomWithPrefix("tf-acc")
	res := "iothub_configuration.test"
	cfg := func(priority int) string {
		return iotacc.ProviderConfig() + fmt.Sprintf(`
resource "iothub_configuration" "test" {
  configuration_id = %q
  target_condition = "*"
  priority         = %d
  labels           = { owner = "platform" }
  device_content   = jsonencode({ "properties.desired.x" = 1 })
}`, id, priority)
	}
	externalPut := func(priority int64, labels map[string]string) {
		c := iotacc.Client(t)
		cur, err := c.GetConfiguration(context.Background(), id)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		spec := client.ConfigurationSpec{ID: id, TargetCondition: cur.TargetCondition, Priority: priority, Labels: labels, Content: cur.Content}
		if _, err := c.UpdateConfiguration(context.Background(), spec, cur.ETag); err != nil {
			t.Fatalf("out-of-band update: %v", err)
		}
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { iotacc.PreCheck(t) },
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactories,
		CheckDestroy:             checkConfigurationDestroyed(id),
		Steps: []resource.TestStep{
			{Config: cfg(1)},
			{ // between plan and apply someone changes a managed field: 412 -> field-level error
				Config: cfg(2),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					sideEffect(func() { externalPut(50, map[string]string{"owner": "someone-else"}) }),
				}},
				ExpectError: regexp.MustCompile(`(?s)changed outside Terraform.*priority: 1 → 50.*labels`),
			},
			{ // a fresh plan sees the external values and converges
				Config:           cfg(2),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)}},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("priority"), knownvalue.Int64Exact(2)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("labels").AtMapKey("owner"), knownvalue.StringExact("platform")),
				},
			},
			{ // an external write that changes nothing Terraform manages only moves the ETag: retried transparently
				Config: cfg(3),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					sideEffect(func() { externalPut(2, map[string]string{"owner": "platform"}) }),
				}},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("priority"), knownvalue.Int64Exact(3)),
				},
			},
			{ // deleted outside Terraform: refresh drops it, plan re-creates it
				PreConfig: func() {
					if err := iotacc.Client(t).DeleteConfiguration(context.Background(), id, "*"); err != nil {
						t.Fatalf("out-of-band delete: %v", err)
					}
				},
				Config:           cfg(3),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionCreate)}},
			},
			{ // duplicate ID -> import hint
				Config: cfg(3) + fmt.Sprintf(`
resource "iothub_configuration" "dup" {
  configuration_id = %q
  target_condition = "*"
  device_content   = jsonencode({ "properties.desired.x" = 1 })
}`, id),
				ExpectError: regexp.MustCompile(`Configuration already exists`),
			},
		},
	})
}

func TestAccConfiguration_moduleContentDataSourcesAndPlanValidation(t *testing.T) {
	id := acctest.RandomWithPrefix("tf-acc")
	cfg := iotacc.ProviderConfig() + fmt.Sprintf(`
resource "iothub_configuration" "mod" {
  configuration_id = %q
  target_condition = "FROM devices.modules WHERE moduleId = 'telemetry'"
  priority         = 5
  module_content   = jsonencode({ "properties.desired.interval" = 30 })
}
data "iothub_configuration" "mod" {
  configuration_id = iothub_configuration.mod.configuration_id
}`, id)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { iotacc.PreCheck(t) },
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactories,
		CheckDestroy:             checkConfigurationDestroyed(id),
		Steps: []resource.TestStep{
			{
				Config: cfg,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("iothub_configuration.mod", tfjsonpath.New("device_content"), knownvalue.Null()),
					statecheck.ExpectKnownValue("iothub_configuration.mod", tfjsonpath.New("schema_version"), knownvalue.Null()),
					statecheck.ExpectKnownValue("data.iothub_configuration.mod", tfjsonpath.New("module_content"), knownvalue.StringExact(`{"properties.desired.interval":30}`)),
					statecheck.ExpectKnownValue("data.iothub_configuration.mod", tfjsonpath.New("device_content"), knownvalue.Null()),
					statecheck.ExpectKnownValue("data.iothub_configuration.mod", tfjsonpath.New("priority"), knownvalue.Int64Exact(5)),
					statecheck.ExpectKnownValue("data.iothub_configuration.mod", tfjsonpath.New("labels"), knownvalue.MapSizeExact(0)),
					statecheck.ExpectKnownValue("data.iothub_configuration.mod", tfjsonpath.New("system_metrics"), knownvalue.NotNull()),
				},
			},
			{
				Config:           cfg,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
			{ // plan-time validation: bad target condition (desired properties are not allowed)
				Config: iotacc.ProviderConfig() + fmt.Sprintf(`
resource "iothub_configuration" "bad" {
  configuration_id = "%s-bad"
  target_condition = "properties.desired.x = 1"
  device_content   = jsonencode({ "properties.desired.x" = 1 })
}`, id),
				ExpectError: regexp.MustCompile(`Invalid target condition`),
			},
			{ // plan-time validation: bad metric query, with the "*" target that testQueries itself rejects
				Config: iotacc.ProviderConfig() + fmt.Sprintf(`
resource "iothub_configuration" "bad" {
  configuration_id = "%s-bad"
  target_condition = "*"
  metrics          = { broken = "SELEC nope" }
  device_content   = jsonencode({ "properties.desired.x" = 1 })
}`, id),
				ExpectError: regexp.MustCompile(`Invalid metric query`),
			},
			{
				Config:      iotacc.ProviderConfig() + `data "iothub_configuration" "missing" { configuration_id = "tf-acc-does-not-exist" }`,
				ExpectError: regexp.MustCompile(`configuration not found`),
			},
		},
	})
}

const edgeManifest = `{
  "$edgeAgent": {
    "properties.desired": {
      "schemaVersion": "1.1",
      "runtime": { "type": "docker", "settings": { "minDockerVersion": "v1.25" } },
      "systemModules": {
        "edgeAgent": { "type": "docker", "settings": { "image": "mcr.microsoft.com/azureiotedge-agent:1.4" } },
        "edgeHub":   { "type": "docker", "status": "running", "restartPolicy": "always", "settings": { "image": "mcr.microsoft.com/azureiotedge-hub:1.4" } }
      },
      "modules": {}
    }
  },
  "$edgeHub": {
    "properties.desired": {
      "schemaVersion": "1.2",
      "routes": { "all": "FROM /messages/* INTO $upstream" },
      "storeAndForwardConfiguration": { "timeToLiveSecs": 7200 }
    }
  }
}`

func TestAccEdgeDeployment_basic(t *testing.T) {
	id := acctest.RandomWithPrefix("tf-acc")
	res := "iothub_edge_deployment.base"
	cfg := func(priority int, manifest string) string {
		return iotacc.ProviderConfig() + fmt.Sprintf(`
locals {
  manifest = %s
}
resource "iothub_edge_deployment" "base" {
  deployment_id    = %q
  target_condition = "tags.site = 'munich'"
  priority         = %d
  labels           = { release = "1" }
  modules_content  = jsonencode(jsondecode(local.manifest))
}
resource "iothub_edge_deployment" "layer" {
  deployment_id    = "%s-layer"
  target_condition = "tags.site = 'munich'"
  priority         = %d
  modules_content = jsonencode({
    "$edgeAgent" = {
      "properties.desired.modules.tempSensor" = {
        type = "docker", status = "running", restartPolicy = "always"
        settings = { image = "mcr.microsoft.com/azureiotedge-simulated-temperature-sensor:1.4" }
      }
    }
  })
}
data "iothub_edge_deployment" "base" {
  deployment_id = iothub_edge_deployment.base.deployment_id
}
data "iothub_edge_deployment" "layer" {
  deployment_id = iothub_edge_deployment.layer.deployment_id
}`, "<<EOT\n"+manifest+"\nEOT", id, priority, id, priority+1)
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { iotacc.PreCheck(t) },
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactories,
		CheckDestroy:             checkConfigurationDestroyed(id, id+"-layer"),
		Steps: []resource.TestStep{
			{
				Config: cfg(10, edgeManifest),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("id"), knownvalue.StringExact(id)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("priority"), knownvalue.Int64Exact(10)),
					statecheck.ExpectKnownValue("data.iothub_edge_deployment.base", tfjsonpath.New("modules_content"), knownvalue.StringRegexp(regexp.MustCompile(`"\$edgeAgent":\{"properties.desired":\{"modules":\{\},"runtime"`))),
					statecheck.ExpectKnownValue("data.iothub_edge_deployment.layer", tfjsonpath.New("priority"), knownvalue.Int64Exact(11)),
				},
				Check: func(_ *terraform.State) error {
					c, err := iotacc.Client(t).GetConfiguration(context.Background(), id)
					if err != nil {
						return err
					}
					var mc map[string]any
					if err := json.Unmarshal(c.Content.ModulesContent, &mc); err != nil {
						return err
					}
					if _, ok := mc["$edgeAgent"]; !ok || len(mc) != 2 {
						return fmt.Errorf("modulesContent on hub: %s", c.Content.ModulesContent)
					}
					if c.SystemMetrics == nil || c.SystemMetrics.Queries["reportedSuccessfulCount"] == "" {
						return fmt.Errorf("edge system metric queries missing: %+v", c.SystemMetrics)
					}
					return nil
				},
			},
			{
				Config:           cfg(10, edgeManifest),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
			{ // priority update in place
				Config: cfg(12, edgeManifest),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate),
					plancheck.ExpectResourceAction("iothub_edge_deployment.layer", plancheck.ResourceActionUpdate),
				}},
			},
			{ // manifest change: replacement
				Config:           cfg(12, regexp.MustCompile(`7200`).ReplaceAllString(edgeManifest, "3600")),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionReplace)}},
			},
			{
				ResourceName:            res,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"timeouts", "system_metrics", "metric_results", "last_updated_time"},
			},
			{ // an edge deployment cannot be read as a configuration
				Config: cfg(12, regexp.MustCompile(`7200`).ReplaceAllString(edgeManifest, "3600")) + `
data "iothub_configuration" "wrong" {
  configuration_id = iothub_edge_deployment.base.deployment_id
}`,
				ExpectError: regexp.MustCompile(`Wrong data source for this configuration`),
			},
		},
	})
}

func TestAccConfiguration_configValidation(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `resource "iothub_configuration" "bad" {
  configuration_id = "Has-Upper"
  target_condition = "*"
  device_content   = jsonencode({ "properties.desired.x" = 1 })
}`,
				ExpectError: regexp.MustCompile(`lowercase only`),
			},
			{
				Config: `resource "iothub_configuration" "bad" {
  configuration_id = "x"
  target_condition = "*"
}`,
				ExpectError: regexp.MustCompile(`(?s)one \(and only one\) of\s+\[device_content,module_content\]`),
			},
			{
				Config: `resource "iothub_configuration" "bad" {
  configuration_id = "x"
  target_condition = "*"
  device_content   = jsonencode({ "properties.desired.x" = 1 })
  module_content   = jsonencode({ "properties.desired.x" = 1 })
}`,
				ExpectError: regexp.MustCompile(`(?s)only one\) of\s+\[device_content,module_content\]`),
			},
			{
				Config: `resource "iothub_configuration" "bad" {
  configuration_id = "x"
  target_condition = "*"
  device_content   = jsonencode({ "tags.site" = "x" })
}`,
				ExpectError: regexp.MustCompile(`must be .properties.desired. or start with`),
			},
			{
				Config: `resource "iothub_edge_deployment" "bad" {
  deployment_id    = "x"
  target_condition = "*"
  modules_content  = jsonencode({ "$edgeHub" = { "properties.desired" = {} } })
}`,
				ExpectError: regexp.MustCompile(`must contain .\$edgeAgent.`),
			},
			{
				Config: `resource "iothub_edge_deployment" "bad" {
  deployment_id    = "x"
  target_condition = "*"
  priority         = -1
  modules_content  = jsonencode({ "$edgeAgent" = { "properties.desired" = {} } })
}`,
				ExpectError: regexp.MustCompile(`must be at least 0`),
			},
		},
	})
}
