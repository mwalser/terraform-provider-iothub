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
)

func TestAccModule_basic(t *testing.T) {
	dev := acctest.RandomWithPrefix("tf-acc")
	res := "iothub_module.test"
	cfg := func(extra string) string {
		return iotacc.ProviderConfig() + fmt.Sprintf(`
resource "iothub_device" "test" {
  device_id = %q
}
resource "iothub_module" "test" {
  device_id = iothub_device.test.device_id
  module_id = "telemetry"
%s
}`, dev, extra)
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { iotacc.PreCheck(t) },
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactories,
		CheckDestroy:             iotacc.CheckDeviceDestroyed(dev),
		Steps: []resource.TestStep{
			{ // defaults: sas with hub-generated keys, no managed_by
				Config: cfg(""),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("id"), knownvalue.StringExact(dev+"/telemetry")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("managed_by"), knownvalue.Null()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("authentication").AtMapKey("type"), knownvalue.StringExact("sas")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("authentication").AtMapKey("primary_key"), knownvalue.StringRegexp(regexp.MustCompile(`^[A-Za-z0-9+/]{43}=$`))),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("authentication").AtMapKey("secondary_key"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("etag"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("generation_id"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("connection_state"), knownvalue.StringExact("Disconnected")),
				},
			},
			{
				Config:           cfg(""),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
			{ // in-place update of managed_by; keys survive the full-body PUT
				Config:           cfg(`  managed_by = "operator"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)}},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("managed_by"), knownvalue.StringExact("operator")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("authentication").AtMapKey("primary_key"), knownvalue.NotNull()),
				},
			},
			{ // switch to X.509 CA and back to managed_by-less
				Config: cfg(`  authentication = { type = "certificateAuthority" }`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("managed_by"), knownvalue.Null()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("authentication").AtMapKey("type"), knownvalue.StringExact("certificateAuthority")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("authentication").AtMapKey("primary_key"), knownvalue.Null()),
				},
			},
			{
				ResourceName:            res,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: importIgnore,
			},
		},
	})
}

func TestAccModule_writeOnlyKeysCredentialsAndList(t *testing.T) {
	dev := acctest.RandomWithPrefix("tf-acc")
	res := "iothub_module.test"
	k1 := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	k2 := "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBA="
	config := func(key string, version int, managedBy string) string {
		return iotacc.ProviderConfig() + fmt.Sprintf(`
resource "iothub_device" "test" {
  device_id = %q
}
resource "iothub_module" "test" {
  device_id                = iothub_device.test.device_id
  module_id                = "m1"
  managed_by               = %q
  primary_key_wo           = %q
  primary_key_wo_version   = %d
  secondary_key_wo         = "CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCA="
  secondary_key_wo_version = 1
}
resource "iothub_module" "other" {
  device_id  = iothub_device.test.device_id
  module_id  = "m2"
  managed_by = "someone"
  authentication = { type = "certificateAuthority" }
}

ephemeral "iothub_module_credentials" "test" {
  device_id = iothub_module.test.device_id
  module_id = iothub_module.test.module_id
}

provider "echo" {
  data = ephemeral.iothub_module_credentials.test
}

resource "echo" "creds" {}

data "iothub_modules" "all" {
  device_id  = iothub_device.test.device_id
  depends_on = [iothub_module.test, iothub_module.other]
}
data "iothub_module" "other" {
  device_id = iothub_module.other.device_id
  module_id = iothub_module.other.module_id
}
`, dev, managedBy, key, version)
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { iotacc.PreCheck(t) },
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactoriesWithEcho,
		CheckDestroy:             iotacc.CheckDeviceDestroyed(dev),
		Steps: []resource.TestStep{
			{
				Config: config(k1, 1, "platform"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("primary_key_wo"), knownvalue.Null()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("authentication").AtMapKey("primary_key"), knownvalue.Null()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("authentication").AtMapKey("secondary_key"), knownvalue.Null()),
					statecheck.ExpectKnownValue("echo.creds", tfjsonpath.New("data").AtMapKey("primary_key"), knownvalue.StringExact(k1)),
					statecheck.ExpectKnownValue("echo.creds", tfjsonpath.New("data").AtMapKey("primary_connection_string"),
						knownvalue.StringExact("HostName="+iotacc.Hostname()+";DeviceId="+dev+";ModuleId=m1;SharedAccessKey="+k1)),
					// list data source sees both modules, without keys
					statecheck.ExpectKnownValue("data.iothub_modules.all", tfjsonpath.New("modules"), knownvalue.ListSizeExact(2)),
					statecheck.ExpectKnownValue("data.iothub_modules.all", tfjsonpath.New("id"), knownvalue.StringExact(dev)),
					statecheck.ExpectKnownValue("data.iothub_module.other", tfjsonpath.New("managed_by"), knownvalue.StringExact("someone")),
					statecheck.ExpectKnownValue("data.iothub_module.other", tfjsonpath.New("authentication").AtMapKey("type"), knownvalue.StringExact("certificateAuthority")),
				},
			},
			{
				Config:           config(k1, 1, "platform"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionNoop)}},
			},
			{ // rotate; verified against the API
				Config:           config(k2, 2, "platform"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)}},
				Check: func(_ *terraform.State) error {
					mod, err := iotacc.Client(t).GetModule(context.Background(), dev, "m1")
					if err != nil {
						return err
					}
					if got := mod.Authentication.SymmetricKey.PrimaryKey; got != k2 {
						return fmt.Errorf("primary key after rotation = %q, want %q", got, k2)
					}
					return nil
				},
			},
			{ // unrelated update with a changed write-only value but the same version: the hub key must stay
				Config:           config(k1, 2, "operator"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)}},
				Check: func(_ *terraform.State) error {
					mod, err := iotacc.Client(t).GetModule(context.Background(), dev, "m1")
					if err != nil {
						return err
					}
					if got := mod.Authentication.SymmetricKey.PrimaryKey; got != k2 {
						return fmt.Errorf("primary key after an unrelated update = %q, want the unchanged %q", got, k2)
					}
					return nil
				},
			},
		},
	})
}

func TestAccModule_disappearsAndErrors(t *testing.T) {
	dev := acctest.RandomWithPrefix("tf-acc")
	res := "iothub_module.test"
	cfg := iotacc.ProviderConfig() + fmt.Sprintf(`
resource "iothub_device" "test" {
  device_id = %q
}
resource "iothub_module" "test" {
  device_id = iothub_device.test.device_id
  module_id = "m1"
}`, dev)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { iotacc.PreCheck(t) },
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactories,
		CheckDestroy:             iotacc.CheckDeviceDestroyed(dev),
		Steps: []resource.TestStep{
			{Config: cfg},
			{ // module deleted outside Terraform -> re-created
				PreConfig: func() {
					if err := iotacc.Client(t).DeleteModule(context.Background(), dev, "m1", "*"); err != nil {
						t.Fatalf("out-of-band delete: %v", err)
					}
				},
				Config:           cfg,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionCreate)}},
			},
			{ // duplicate -> import hint
				Config: cfg + fmt.Sprintf(`
resource "iothub_module" "dup" {
  device_id = %q
  module_id = "m1"
}`, dev),
				ExpectError: regexp.MustCompile(`Module already exists`),
			},
			{ // module on a device that does not exist
				Config: iotacc.ProviderConfig() + `
resource "iothub_module" "orphan" {
  device_id = "tf-acc-does-not-exist"
  module_id = "m1"
}`,
				ExpectError: regexp.MustCompile(`Device not found`),
			},
		},
	})
}

func TestAccModule_configValidation(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `resource "iothub_module" "bad" {
  device_id = "d"
  module_id = "$edgeAgent"
}`,
				ExpectError: regexp.MustCompile(`must not start with \$`),
			},
			{
				Config: `resource "iothub_module" "bad" {
  device_id = "d"
  module_id = "m"
  authentication = { type = "selfSigned" }
}`,
				ExpectError: regexp.MustCompile(`selfSigned authentication needs a thumbprint`),
			},
			{
				Config: `resource "iothub_module" "bad" {
  device_id      = "d"
  module_id      = "m"
  primary_key_wo = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
}`,
				ExpectError: regexp.MustCompile(`primary_key_wo_version`),
			},
		},
	})
}
