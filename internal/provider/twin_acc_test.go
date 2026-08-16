package provider_test

import (
	"context"
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
	"github.com/mwalser/terraform-provider-iothub/internal/twinpatch"
)

// twinSections reads a device twin's tags and desired properties (system
// keys stripped) through the API.
func twinSections(t *testing.T, deviceID string) (tags, desired map[string]any) {
	t.Helper()
	tw, err := iotacc.Client(t).GetDeviceTwin(context.Background(), deviceID)
	if err != nil {
		t.Fatalf("reading twin: %v", err)
	}
	tags, err = twinpatch.Decode(string(tw.Tags))
	if err != nil {
		t.Fatalf("tags: %v", err)
	}
	desired, err = twinpatch.Decode(string(tw.Properties.Desired))
	if err != nil {
		t.Fatalf("desired: %v", err)
	}
	return tags, twinpatch.StripSystem(desired)
}

// expectTwin asserts the exact content of both sections through the API.
func expectTwin(t *testing.T, deviceID, wantTags, wantDesired string) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		tags, desired := twinSections(t, deviceID)
		wt, _ := twinpatch.Decode(wantTags)
		wd, _ := twinpatch.Decode(wantDesired)
		if !twinpatch.Equal(tags, wt) {
			return fmt.Errorf("tags = %s, want %s", twinpatch.Encode(tags), wantTags)
		}
		if !twinpatch.Equal(desired, wd) {
			return fmt.Errorf("desired = %s, want %s", twinpatch.Encode(desired), wantDesired)
		}
		return nil
	}
}

func patchTwin(t *testing.T, deviceID, tags, desired string) {
	t.Helper()
	var p client.TwinPatch
	var err error
	if tags != "" {
		if p.Tags, err = twinpatch.Decode(tags); err != nil {
			t.Fatal(err)
		}
	}
	if desired != "" {
		if p.Desired, err = twinpatch.Decode(desired); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := iotacc.Client(t).PatchDeviceTwin(context.Background(), deviceID, p); err != nil {
		t.Fatalf("out-of-band twin patch: %v", err)
	}
}

func TestAccDeviceTwin_leafOwnership(t *testing.T) {
	dev := acctest.RandomWithPrefix("tf-acc")
	res := "iothub_device_twin.test"
	cfg := func(tags, desired string) string {
		body := ""
		if tags != "" {
			body += "  tags = " + tags + "\n"
		}
		if desired != "" {
			body += "  desired_properties = " + desired + "\n"
		}
		return iotacc.ProviderConfig() + fmt.Sprintf(`
resource "iothub_device" "test" {
  device_id = %q
}
resource "iothub_device_twin" "test" {
  device_id = iothub_device.test.device_id
%s}`, dev, body)
	}
	tags1 := `jsonencode({ site = "munich", fleet = { region = "eu", ring = 2 } })`
	desired1 := `jsonencode({ telemetryIntervalSec = 60, firmware = { channel = "stable" } })`
	var etagAfterCreate string
	rememberETag := func(_ *terraform.State) error {
		tw, err := iotacc.Client(t).GetDeviceTwin(context.Background(), dev)
		if err != nil {
			return err
		}
		etagAfterCreate = tw.ETag
		return nil
	}
	expectSameETag := func(_ *terraform.State) error {
		tw, err := iotacc.Client(t).GetDeviceTwin(context.Background(), dev)
		if err != nil {
			return err
		}
		if tw.ETag != etagAfterCreate {
			return fmt.Errorf("twin was written (etag %s -> %s) although nothing changed semantically", etagAfterCreate, tw.ETag)
		}
		return nil
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { iotacc.PreCheck(t) },
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactories,
		CheckDestroy:             iotacc.CheckDeviceDestroyed(dev),
		Steps: []resource.TestStep{
			{
				Config: cfg(tags1, desired1),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("id"), knownvalue.StringExact(dev)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("tags"), knownvalue.StringExact(`{"fleet":{"region":"eu","ring":2},"site":"munich"}`)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("etag"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("version"), knownvalue.NotNull()),
				},
				Check: resource.ComposeTestCheckFunc(
					expectTwin(t, dev, `{"fleet":{"region":"eu","ring":2},"site":"munich"}`, `{"telemetryIntervalSec":60,"firmware":{"channel":"stable"}}`),
					rememberETag,
				),
			},
			{ // cosmetic differences (key order, whitespace, 2 vs 2.0): Terraform plans an
				// update of the string, but nothing is written to the twin
				Config:           cfg(`"{\"site\":\"munich\", \"fleet\": {\"ring\": 2.0, \"region\": \"eu\"}}"`, desired1),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)}},
				Check:            expectSameETag,
			},
			{ // and back: same
				Config:           cfg(tags1, desired1),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)}},
				Check:            expectSameETag,
			},
			{ // foreign keys next to owned ones are invisible
				PreConfig: func() {
					patchTwin(t, dev, `{"owner":"ops","fleet":{"lastCheck":"2026-08-15"}}`, `{"firmware":{"lastCheck":"2026-08-15"},"other":{"x":1}}`)
				},
				Config:           cfg(tags1, desired1),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
			{ // change a leaf, drop a top-level leaf, drop an object with a foreign sibling: leaf-level nulls only
				Config:           cfg(`jsonencode({ fleet = { region = "eu", ring = 3 } })`, `jsonencode({ telemetryIntervalSec = 60 })`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)}},
				Check: expectTwin(t, dev,
					`{"owner":"ops","fleet":{"region":"eu","ring":3,"lastCheck":"2026-08-15"}}`,
					`{"telemetryIntervalSec":60,"firmware":{"lastCheck":"2026-08-15"},"other":{"x":1}}`),
			},
			{ // stop managing tags entirely: fleet keeps the foreign lastCheck; desired untouched
				Config: cfg("", `jsonencode({ telemetryIntervalSec = 60 })`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("tags"), knownvalue.Null()),
				},
				Check: expectTwin(t, dev,
					`{"owner":"ops","fleet":{"lastCheck":"2026-08-15"}}`,
					`{"telemetryIntervalSec":60,"firmware":{"lastCheck":"2026-08-15"},"other":{"x":1}}`),
			},
			{ // destroy the twin resource only: owned leaves nulled, everything foreign survives, device stays
				Config: iotacc.ProviderConfig() + fmt.Sprintf(`
resource "iothub_device" "test" {
  device_id = %q
}`, dev),
				Check: expectTwin(t, dev,
					`{"owner":"ops","fleet":{"lastCheck":"2026-08-15"}}`,
					`{"firmware":{"lastCheck":"2026-08-15"},"other":{"x":1}}`),
			},
		},
	})
}

func TestAccDeviceTwin_driftAndAncestorPruning(t *testing.T) {
	dev := acctest.RandomWithPrefix("tf-acc")
	res := "iothub_device_twin.test"
	cfg := func(tags string) string {
		return iotacc.ProviderConfig() + fmt.Sprintf(`
resource "iothub_device" "test" {
  device_id = %q
}
resource "iothub_device_twin" "test" {
  device_id = iothub_device.test.device_id
  tags      = %s
}`, dev, tags)
	}
	full := `jsonencode({ fw = { a = 1, b = [1, 2] }, empty = {}, site = "x" })`
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { iotacc.PreCheck(t) },
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactories,
		CheckDestroy:             iotacc.CheckDeviceDestroyed(dev),
		Steps: []resource.TestStep{
			{
				Config: cfg(full),
				Check:  expectTwin(t, dev, `{"fw":{"a":1,"b":[1,2]},"empty":{},"site":"x"}`, `{}`),
			},
			{ // external change of an owned leaf and deletion of another: drift -> restored
				PreConfig: func() { patchTwin(t, dev, `{"fw":{"a":99,"b":null}}`, "") },
				Config:    cfg(full),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate),
				}},
				Check: expectTwin(t, dev, `{"fw":{"a":1,"b":[1,2]},"empty":{},"site":"x"}`, `{}`),
			},
			{ // foreign content inside the {} leaf is fine (the leaf asserts "an object"), no diff
				PreConfig:        func() { patchTwin(t, dev, `{"empty":{"foreign":true}}`, "") },
				Config:           cfg(full),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
			{ // removing fw (Terraform was its only writer) deletes the whole object, not just its leaves
				Config: cfg(`jsonencode({ empty = {}, site = "x" })`),
				Check: func(s *terraform.State) error {
					tags, _ := twinSections(t, dev)
					if _, ok := tags["fw"]; ok {
						return fmt.Errorf("fw should have been removed entirely, tags = %s", twinpatch.Encode(tags))
					}
					return expectTwin(t, dev, `{"empty":{"foreign":true},"site":"x"}`, `{}`)(s)
				},
			},
			{ // removing the {} leaf leaves the foreign content alone
				Config: cfg(`jsonencode({ site = "x" })`),
				Check:  expectTwin(t, dev, `{"empty":{"foreign":true},"site":"x"}`, `{}`),
			},
			{ // device deleted outside Terraform: both resources are re-created
				PreConfig: func() {
					if err := iotacc.Client(t).DeleteDevice(context.Background(), dev, "*"); err != nil {
						t.Fatalf("out-of-band delete: %v", err)
					}
				},
				Config: cfg(`jsonencode({ site = "x" })`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("iothub_device.test", plancheck.ResourceActionCreate),
					plancheck.ExpectResourceAction(res, plancheck.ResourceActionCreate),
				}},
				Check: expectTwin(t, dev, `{"site":"x"}`, `{}`),
			},
		},
	})
}

func TestAccDeviceTwin_importAdopts(t *testing.T) {
	dev := acctest.RandomWithPrefix("tf-acc")
	res := "iothub_device_twin.test"
	deviceOnly := iotacc.ProviderConfig() + fmt.Sprintf(`
resource "iothub_device" "test" {
  device_id = %q
}`, dev)
	withTwin := deviceOnly + `
resource "iothub_device_twin" "test" {
  device_id = iothub_device.test.device_id
  tags      = jsonencode({ site = "munich", fleet = { ring = 1 } })
}`
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { iotacc.PreCheck(t) },
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactories,
		CheckDestroy:             iotacc.CheckDeviceDestroyed(dev),
		Steps: []resource.TestStep{
			{ // a device whose twin already carries content nobody in Terraform owns
				Config: deviceOnly,
				Check: func(_ *terraform.State) error {
					patchTwin(t, dev, `{"site":"berlin","fleet":{"region":"eu"},"legacy":true}`, "")
					return nil
				},
			},
			{ // import: empty owned set
				Config:             withTwin,
				ResourceName:       res,
				ImportState:        true,
				ImportStateId:      dev,
				ImportStatePersist: true,
				ImportStateCheck: func(states []*terraform.InstanceState) error {
					for _, st := range states {
						if st.Attributes["id"] != dev {
							continue
						}
						if v, ok := st.Attributes["tags"]; ok && v != "" {
							return fmt.Errorf("imported twin must own nothing, got tags %q", v)
						}
						return nil
					}
					return fmt.Errorf("imported twin not found in %d states", len(states))
				},
			},
			{ // first apply adopts the configured leaves; foreign keys survive
				Config:           withTwin,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)}},
				Check:            expectTwin(t, dev, `{"site":"munich","fleet":{"ring":1,"region":"eu"},"legacy":true}`, `{}`),
			},
			{
				Config:           withTwin,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
		},
	})
}

func TestAccModuleTwin_basicAndDataSources(t *testing.T) {
	dev := acctest.RandomWithPrefix("tf-acc")
	res := "iothub_module_twin.test"
	cfg := iotacc.ProviderConfig() + fmt.Sprintf(`
resource "iothub_device" "test" {
  device_id = %q
}
resource "iothub_module" "test" {
  device_id = iothub_device.test.device_id
  module_id = "m1"
}
resource "iothub_module_twin" "test" {
  device_id          = iothub_module.test.device_id
  module_id          = iothub_module.test.module_id
  desired_properties = jsonencode({ interval = 30, nested = { flag = true } })
}
resource "iothub_device_twin" "test" {
  device_id = iothub_device.test.device_id
  tags      = jsonencode({ site = "munich" })
}
data "iothub_module_twin" "test" {
  device_id  = iothub_module_twin.test.device_id
  module_id  = iothub_module_twin.test.module_id
}
data "iothub_device_twin" "test" {
  device_id  = iothub_device_twin.test.device_id
}`, dev)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { iotacc.PreCheck(t) },
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactories,
		CheckDestroy:             iotacc.CheckDeviceDestroyed(dev),
		Steps: []resource.TestStep{
			{
				Config: cfg,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("id"), knownvalue.StringExact(dev+"/m1")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("tags"), knownvalue.Null()),
					statecheck.ExpectKnownValue("data.iothub_module_twin.test", tfjsonpath.New("desired_properties"), knownvalue.StringExact(`{"interval":30,"nested":{"flag":true}}`)),
					statecheck.ExpectKnownValue("data.iothub_module_twin.test", tfjsonpath.New("reported_properties"), knownvalue.StringExact(`{}`)),
					statecheck.ExpectKnownValue("data.iothub_module_twin.test", tfjsonpath.New("desired_version"), knownvalue.Int64Exact(2)),
					statecheck.ExpectKnownValue("data.iothub_module_twin.test", tfjsonpath.New("reported_version"), knownvalue.Int64Exact(1)),
					statecheck.ExpectKnownValue("data.iothub_module_twin.test", tfjsonpath.New("tags"), knownvalue.StringExact(`{}`)),
					statecheck.ExpectKnownValue("data.iothub_device_twin.test", tfjsonpath.New("tags"), knownvalue.StringExact(`{"site":"munich"}`)),
					statecheck.ExpectKnownValue("data.iothub_device_twin.test", tfjsonpath.New("status"), knownvalue.StringExact("enabled")),
					statecheck.ExpectKnownValue("data.iothub_device_twin.test", tfjsonpath.New("device_etag"), knownvalue.NotNull()),
				},
			},
			{
				Config:           cfg,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
			{
				ResourceName:            res,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"desired_properties"}, // import owns nothing
			},
			{
				Config: iotacc.ProviderConfig() + `
resource "iothub_device_twin" "missing" {
  device_id = "tf-acc-does-not-exist"
  tags      = jsonencode({ a = 1 })
}`,
				ExpectError: regexp.MustCompile(`device twin not found`),
			},
		},
	})
}

func TestAccDeviceTwin_configValidation(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `resource "iothub_device_twin" "bad" {
  device_id = "d"
  tags      = jsonencode({ "a.b" = 1 })
}`,
				ExpectError: regexp.MustCompile(`must not contain`),
			},
			{
				Config: `resource "iothub_device_twin" "bad" {
  device_id          = "d"
  desired_properties = jsonencode({ a = null })
}`,
				ExpectError: regexp.MustCompile(`null is not a twin value`),
			},
			{
				Config: `resource "iothub_device_twin" "bad" {
  device_id = "d"
  tags      = jsonencode([1, 2])
}`,
				ExpectError: regexp.MustCompile(`expected a JSON object`),
			},
			{
				Config: `resource "iothub_device_twin" "bad" {
  device_id = "d"
  tags      = "not json"
}`,
				ExpectError: regexp.MustCompile(`Invalid JSON document`),
			},
		},
	})
}
