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

// volatile identity attributes that a fresh read may legitimately change.
var importIgnore = []string{"connection_state", "connection_state_updated_time", "last_activity_time", "cloud_to_device_message_count", "timeouts"}

func TestAccDevice_basic(t *testing.T) {
	id := acctest.RandomWithPrefix("tf-acc")
	res := "iothub_device.test"
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { iotacc.PreCheck(t) },
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactories,
		CheckDestroy:             iotacc.CheckDeviceDestroyed(id),
		Steps: []resource.TestStep{
			{ // create with every default: enabled, not edge, sas with hub-generated keys
				Config: iotacc.ProviderConfig() + fmt.Sprintf(`
resource "iothub_device" "test" {
  device_id = %q
}`, id),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("id"), knownvalue.StringExact(iotacc.Hostname()+"/devices/"+id)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("hostname"), knownvalue.StringExact(iotacc.Hostname())),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("status"), knownvalue.StringExact("enabled")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("status_reason"), knownvalue.Null()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("edge_enabled"), knownvalue.Bool(false)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("parent_scope"), knownvalue.Null()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("device_scope"), knownvalue.Null()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("authentication").AtMapKey("type"), knownvalue.StringExact("sas")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("authentication").AtMapKey("primary_key"), knownvalue.StringRegexp(regexp.MustCompile(`^[A-Za-z0-9+/]{43}=$`))),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("authentication").AtMapKey("secondary_key"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("authentication").AtMapKey("primary_thumbprint"), knownvalue.Null()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("etag"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("generation_id"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("connection_state"), knownvalue.StringExact("Disconnected")),
				},
			},
			{ // plan is empty afterwards (no perpetual diff from computed auth / defaults)
				Config: iotacc.ProviderConfig() + fmt.Sprintf(`
resource "iothub_device" "test" {
  device_id = %q
}`, id),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
			{ // in-place update of status/reason; keys survive the full-body PUT
				Config: iotacc.ProviderConfig() + fmt.Sprintf(`
resource "iothub_device" "test" {
  device_id     = %q
  status        = "disabled"
  status_reason = "maintenance window"
}`, id),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)}},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("status"), knownvalue.StringExact("disabled")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("status_reason"), knownvalue.StringExact("maintenance window")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("authentication").AtMapKey("primary_key"), knownvalue.NotNull()),
				},
			},
			{ // import
				ResourceName:            res,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: importIgnore,
			},
		},
	})
}

func TestAccDevice_x509Transitions(t *testing.T) {
	id := acctest.RandomWithPrefix("tf-acc")
	res := "iothub_device.test"
	tp := "aabbccddeeff00112233445566778899aabbccdd"
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { iotacc.PreCheck(t) },
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactories,
		CheckDestroy:             iotacc.CheckDeviceDestroyed(id),
		Steps: []resource.TestStep{
			{
				Config: iotacc.ProviderConfig() + fmt.Sprintf(`
resource "iothub_device" "test" {
  device_id = %q
  authentication = {
    type               = "selfSigned"
    primary_thumbprint = %q
  }
}`, id, tp),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("authentication").AtMapKey("type"), knownvalue.StringExact("selfSigned")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("authentication").AtMapKey("primary_thumbprint"), knownvalue.StringExact(tp)),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("authentication").AtMapKey("secondary_thumbprint"), knownvalue.Null()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("authentication").AtMapKey("primary_key"), knownvalue.Null()),
				},
			},
			{ // switch to CA-signed: thumbprints go away, no keys appear
				Config: iotacc.ProviderConfig() + fmt.Sprintf(`
resource "iothub_device" "test" {
  device_id = %q
  authentication = {
    type = "certificateAuthority"
  }
}`, id),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("authentication").AtMapKey("type"), knownvalue.StringExact("certificateAuthority")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("authentication").AtMapKey("primary_thumbprint"), knownvalue.Null()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("authentication").AtMapKey("primary_key"), knownvalue.Null()),
				},
			},
			{ // switch to sas with user-supplied keys, echoed byte for byte
				Config: iotacc.ProviderConfig() + fmt.Sprintf(`
resource "iothub_device" "test" {
  device_id = %q
  authentication = {
    type          = "sas"
    primary_key   = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
    secondary_key = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBA="
  }
}`, id),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("authentication").AtMapKey("type"), knownvalue.StringExact("sas")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("authentication").AtMapKey("primary_key"), knownvalue.StringExact("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("authentication").AtMapKey("secondary_key"), knownvalue.StringExact("BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBA=")),
				},
			},
			{ // only the primary given: the provider generates the secondary instead of failing
				Config: iotacc.ProviderConfig() + fmt.Sprintf(`
resource "iothub_device" "test" {
  device_id = %q
  authentication = {
    type        = "sas"
    primary_key = "CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCA="
  }
}`, id),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("authentication").AtMapKey("primary_key"), knownvalue.StringExact("CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCA=")),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("authentication").AtMapKey("secondary_key"), knownvalue.StringExact("BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBA=")),
				},
			},
		},
	})
}

func TestAccDevice_edgeAndChildren(t *testing.T) {
	edge := acctest.RandomWithPrefix("tf-acc-edge")
	leaf := acctest.RandomWithPrefix("tf-acc-leaf")
	child := acctest.RandomWithPrefix("tf-acc-child")
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { iotacc.PreCheck(t) },
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactories,
		CheckDestroy:             iotacc.CheckDeviceDestroyed(edge, leaf, child),
		Steps: []resource.TestStep{
			{
				Config: iotacc.ProviderConfig() + fmt.Sprintf(`
resource "iothub_device" "edge" {
  device_id    = %q
  edge_enabled = true
  authentication = { type = "certificateAuthority" }
}
resource "iothub_device" "leaf" {
  device_id    = %q
  parent_scope = iothub_device.edge.device_scope
}
resource "iothub_device" "child" {
  device_id    = %q
  edge_enabled = true
  parent_scope = iothub_device.edge.device_scope
}`, edge, leaf, child),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("iothub_device.edge", tfjsonpath.New("device_scope"), knownvalue.StringRegexp(regexp.MustCompile(`^ms-azure-iot-edge://`+edge+`-\d+$`))),
					statecheck.ExpectKnownValue("iothub_device.edge", tfjsonpath.New("parent_scope"), knownvalue.Null()),
					// leaf: parent scope written to deviceScope; hub also reports it in parentScopes
					statecheck.ExpectKnownValue("iothub_device.leaf", tfjsonpath.New("edge_enabled"), knownvalue.Bool(false)),
					statecheck.ExpectKnownValue("iothub_device.leaf", tfjsonpath.New("parent_scope"), knownvalue.StringRegexp(regexp.MustCompile(`^ms-azure-iot-edge://`+edge))),
					statecheck.ExpectKnownValue("iothub_device.leaf", tfjsonpath.New("device_scope"), knownvalue.StringRegexp(regexp.MustCompile(`^ms-azure-iot-edge://`+edge))),
					statecheck.ExpectKnownValue("iothub_device.leaf", tfjsonpath.New("parent_scopes"), knownvalue.ListSizeExact(1)),
					// nested edge child: own generated scope, parent in parentScopes
					statecheck.ExpectKnownValue("iothub_device.child", tfjsonpath.New("device_scope"), knownvalue.StringRegexp(regexp.MustCompile(`^ms-azure-iot-edge://`+child))),
					statecheck.ExpectKnownValue("iothub_device.child", tfjsonpath.New("parent_scope"), knownvalue.StringRegexp(regexp.MustCompile(`^ms-azure-iot-edge://`+edge))),
				},
			},
			{ // detach both children; leaf turns edge (in-place); no perpetual diff on the edge device
				Config: iotacc.ProviderConfig() + fmt.Sprintf(`
resource "iothub_device" "edge" {
  device_id    = %q
  edge_enabled = true
  authentication = { type = "certificateAuthority" }
}
resource "iothub_device" "leaf" {
  device_id    = %q
  edge_enabled = true
}
resource "iothub_device" "child" {
  device_id    = %q
  edge_enabled = true
}`, edge, leaf, child),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("iothub_device.edge", plancheck.ResourceActionNoop),
					plancheck.ExpectResourceAction("iothub_device.leaf", plancheck.ResourceActionUpdate),
					plancheck.ExpectResourceAction("iothub_device.child", plancheck.ResourceActionUpdate),
				}},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("iothub_device.leaf", tfjsonpath.New("edge_enabled"), knownvalue.Bool(true)),
					statecheck.ExpectKnownValue("iothub_device.leaf", tfjsonpath.New("parent_scope"), knownvalue.Null()),
					statecheck.ExpectKnownValue("iothub_device.child", tfjsonpath.New("parent_scope"), knownvalue.Null()),
					statecheck.ExpectKnownValue("iothub_device.child", tfjsonpath.New("parent_scopes"), knownvalue.ListSizeExact(0)),
				},
			},
		},
	})
}

func TestAccDevice_writeOnlyKeysAndCredentials(t *testing.T) {
	id := acctest.RandomWithPrefix("tf-acc")
	res := "iothub_device.test"
	k1 := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	k2 := "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBA="
	config := func(key string, version int) string {
		return iotacc.ProviderConfig() + fmt.Sprintf(`
resource "iothub_device" "test" {
  device_id              = %q
  primary_key_wo         = %q
  primary_key_wo_version = %d
}

ephemeral "iothub_device_credentials" "test" {
  device_id = iothub_device.test.device_id
}

provider "echo" {
  data = ephemeral.iothub_device_credentials.test
}

resource "echo" "creds" {}
`, id, key, version)
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { iotacc.PreCheck(t) },
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactoriesWithEcho,
		CheckDestroy:             iotacc.CheckDeviceDestroyed(id),
		Steps: []resource.TestStep{
			{
				Config: config(k1, 1),
				ConfigStateChecks: []statecheck.StateCheck{
					// write-only: the primary key is neither in the wo attribute nor in authentication.primary_key
					statecheck.ExpectKnownValue(res, tfjsonpath.New("primary_key_wo"), knownvalue.Null()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("authentication").AtMapKey("primary_key"), knownvalue.Null()),
					// the secondary was hub-generated/provider-generated and is stored as usual
					statecheck.ExpectKnownValue(res, tfjsonpath.New("authentication").AtMapKey("secondary_key"), knownvalue.NotNull()),
					// the ephemeral resource sees the real key
					statecheck.ExpectKnownValue("echo.creds", tfjsonpath.New("data").AtMapKey("primary_key"), knownvalue.StringExact(k1)),
					statecheck.ExpectKnownValue("echo.creds", tfjsonpath.New("data").AtMapKey("authentication_type"), knownvalue.StringExact("sas")),
					statecheck.ExpectKnownValue("echo.creds", tfjsonpath.New("data").AtMapKey("primary_connection_string"),
						knownvalue.StringExact("HostName="+iotacc.Hostname()+";DeviceId="+id+";SharedAccessKey="+k1)),
				},
			},
			{ // same version: no diff even though the plan cannot see the key
				Config:           config(k1, 1),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionNoop)}},
			},
			{ // rotate: bump the version with a new key; secondary is kept.
				// (Verified against the API rather than through the echo provider,
				// whose provider block may be configured before the update applies.)
				Config:           config(k2, 2),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)}},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(res, tfjsonpath.New("authentication").AtMapKey("primary_key"), knownvalue.Null()),
					statecheck.ExpectKnownValue(res, tfjsonpath.New("authentication").AtMapKey("secondary_key"), knownvalue.NotNull()),
				},
				Check: func(_ *terraform.State) error {
					dev, err := iotacc.Client(t).GetDevice(context.Background(), id)
					if err != nil {
						return err
					}
					if got := dev.Authentication.SymmetricKey.PrimaryKey; got != k2 {
						return fmt.Errorf("primary key after rotation = %q, want %q", got, k2)
					}
					return nil
				},
			},
		},
	})
}

func TestAccDevice_dataSource(t *testing.T) {
	id := acctest.RandomWithPrefix("tf-acc")
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { iotacc.PreCheck(t) },
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactories,
		CheckDestroy:             iotacc.CheckDeviceDestroyed(id),
		Steps: []resource.TestStep{
			{
				Config: iotacc.ProviderConfig() + fmt.Sprintf(`
resource "iothub_device" "test" {
  device_id     = %q
  status        = "disabled"
  status_reason = "for the data source"
}
data "iothub_device" "test" {
  device_id = iothub_device.test.device_id
}`, id),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("data.iothub_device.test", tfjsonpath.New("id"), knownvalue.StringExact(iotacc.Hostname()+"/devices/"+id)),
					statecheck.ExpectKnownValue("data.iothub_device.test", tfjsonpath.New("status"), knownvalue.StringExact("disabled")),
					statecheck.ExpectKnownValue("data.iothub_device.test", tfjsonpath.New("status_reason"), knownvalue.StringExact("for the data source")),
					statecheck.ExpectKnownValue("data.iothub_device.test", tfjsonpath.New("authentication_type"), knownvalue.StringExact("sas")),
					statecheck.ExpectKnownValue("data.iothub_device.test", tfjsonpath.New("edge_enabled"), knownvalue.Bool(false)),
				},
			},
			{
				Config:      iotacc.ProviderConfig() + `data "iothub_device" "missing" { device_id = "tf-acc-does-not-exist" }`,
				ExpectError: regexp.MustCompile(`Device not found`),
			},
		},
	})
}

func TestAccDevice_disappears(t *testing.T) {
	id := acctest.RandomWithPrefix("tf-acc")
	res := "iothub_device.test"
	cfg := iotacc.ProviderConfig() + fmt.Sprintf(`
resource "iothub_device" "test" {
  device_id = %q
}`, id)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { iotacc.PreCheck(t) },
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactories,
		CheckDestroy:             iotacc.CheckDeviceDestroyed(id),
		Steps: []resource.TestStep{
			{Config: cfg},
			{ // deleted outside Terraform -> refresh drops it -> plan re-creates it
				PreConfig: func() {
					c := iotacc.Client(t)
					if err := c.DeleteDevice(context.Background(), id, "*"); err != nil {
						t.Fatalf("out-of-band delete: %v", err)
					}
				},
				Config:           cfg,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionCreate)}},
			},
			{ // duplicate ID -> clear error with the import hint
				Config: cfg + fmt.Sprintf(`
resource "iothub_device" "dup" {
  device_id = %q
}`, id),
				ExpectError: regexp.MustCompile(`Device already exists`),
			},
		},
	})
}

func TestAccDevice_configValidation(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `resource "iothub_device" "bad" {
  device_id = "has space"
}`,
				ExpectError: regexp.MustCompile(`must be 1–128 characters`),
			},
			{
				Config: `resource "iothub_device" "bad" {
  device_id = "x"
  authentication = { type = "selfSigned" }
}`,
				ExpectError: regexp.MustCompile(`selfSigned authentication needs a thumbprint`),
			},
			{
				Config: `resource "iothub_device" "bad" {
  device_id = "x"
  authentication = { type = "certificateAuthority", primary_key = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" }
}`,
				ExpectError: regexp.MustCompile(`certificateAuthority takes no keys or thumbprints`),
			},
			{
				Config: `resource "iothub_device" "bad" {
  device_id      = "x"
  primary_key_wo = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
}`,
				ExpectError: regexp.MustCompile(`primary_key_wo_version`),
			},
			{
				Config: `resource "iothub_device" "bad" {
  device_id    = "x"
  parent_scope = "not-a-scope"
}`,
				ExpectError: regexp.MustCompile(`ms-azure-iot-edge://`),
			},
		},
	})
}
