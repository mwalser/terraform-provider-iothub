package provider_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	iotacc "github.com/mwalser/terraform-provider-iothub/internal/acctest"
	"github.com/mwalser/terraform-provider-iothub/internal/client"
)

func TestAccQuery_basic(t *testing.T) {
	marker := acctest.RandomWithPrefix("tfacc")
	dev1, dev2 := marker+"-a", marker+"-b"
	base := iotacc.ProviderConfig() + fmt.Sprintf(`
resource "iothub_device" "a" {
  device_id = %q
}
resource "iothub_device" "b" {
  device_id = %q
}
resource "iothub_device_twin" "a" {
  device_id = iothub_device.a.device_id
  tags      = jsonencode({ tfacc = %q, ring = 1 })
}
resource "iothub_device_twin" "b" {
  device_id = iothub_device.b.device_id
  tags      = jsonencode({ tfacc = %q, ring = 2 })
}`, dev1, dev2, marker, marker)
	query := fmt.Sprintf("SELECT deviceId, tags.ring AS ring FROM devices WHERE tags.tfacc = '%s'", marker)
	withQuery := base + fmt.Sprintf(`
data "iothub_query" "test" {
  query = %q
}
data "iothub_query" "twins" {
  query = "SELECT * FROM devices WHERE deviceId = '%s'"
}`, query, dev1)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { iotacc.PreCheck(t) },
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactories,
		CheckDestroy:             iotacc.CheckDeviceDestroyed(dev1, dev2),
		Steps: []resource.TestStep{
			{Config: base},
			{ // the query index is eventually consistent: wait for both devices to show up
				PreConfig: func() {
					c := iotacc.Client(t)
					deadline := time.Now().Add(90 * time.Second)
					for {
						items, _, err := c.Query(context.Background(), query)
						if err != nil {
							t.Fatalf("query: %v", err)
						}
						if len(items) == 2 || time.Now().After(deadline) {
							if len(items) != 2 {
								t.Fatalf("query index did not catch up: %d items", len(items))
							}
							return
						}
						time.Sleep(3 * time.Second)
					}
				},
				Config: withQuery,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("data.iothub_query.test", tfjsonpath.New("item_type"), knownvalue.StringExact("Raw")),
					statecheck.ExpectKnownValue("data.iothub_query.test", tfjsonpath.New("results"), knownvalue.ListSizeExact(2)),
					statecheck.ExpectKnownValue("data.iothub_query.twins", tfjsonpath.New("item_type"), knownvalue.StringExact("Twin")),
				},
				Check: func(s *terraform.State) error {
					rs := s.RootModule().Resources["data.iothub_query.test"]
					var rows []map[string]any
					for i := 0; i < 2; i++ {
						var row map[string]any
						if err := json.Unmarshal([]byte(rs.Primary.Attributes[fmt.Sprintf("results.%d", i)]), &row); err != nil {
							return err
						}
						rows = append(rows, row)
					}
					seen := map[string]bool{}
					for _, r := range rows {
						id, _ := r["deviceId"].(string)
						seen[id] = true
						if _, ok := r["ring"]; !ok {
							return fmt.Errorf("projection missing ring: %v", r)
						}
					}
					if !seen[dev1] || !seen[dev2] {
						return fmt.Errorf("unexpected rows: %v", rows)
					}
					return nil
				},
			},
			{
				Config:      iotacc.ProviderConfig() + `data "iothub_query" "bad" { query = "SELEC nonsense" }`,
				ExpectError: regexp.MustCompile(`IoT Hub query failed`),
			},
		},
	})
}

// deviceGET performs a device-facing HTTPS call with a device SAS token; the
// cloud-to-device receive endpoint answers 204 (no message) for a valid
// token and 401 otherwise. (Twins and module endpoints are not available
// over the device HTTPS protocol.)
func deviceGET(t *testing.T, path, token string) int {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://"+iotacc.Hostname()+path+"?api-version="+client.APIVersion, nil)
	req.Header.Set("Authorization", token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("device call: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func TestAccDeviceSASToken_deviceAndModule(t *testing.T) {
	dev := acctest.RandomWithPrefix("tf-acc")
	cfg := iotacc.ProviderConfig() + fmt.Sprintf(`
resource "iothub_device" "test" {
  device_id = %q
}
resource "iothub_module" "test" {
  device_id = iothub_device.test.device_id
  module_id = "m1"
}
ephemeral "iothub_sas_token" "dev" {
  device_id = iothub_device.test.device_id
  ttl       = "30m"
}
ephemeral "iothub_sas_token" "mod" {
  device_id = iothub_module.test.device_id
  module_id = iothub_module.test.module_id
  key       = "secondary"
}
provider "echo" {
  data = {
    dev = ephemeral.iothub_sas_token.dev
    mod = ephemeral.iothub_sas_token.mod
  }
}
resource "echo" "tokens" {}
`, dev)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { iotacc.PreCheck(t) },
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactoriesWithEcho,
		CheckDestroy:             iotacc.CheckDeviceDestroyed(dev),
		Steps: []resource.TestStep{
			{
				Config: cfg,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("echo.tokens", tfjsonpath.New("data").AtMapKey("dev").AtMapKey("resource_uri"), knownvalue.StringExact(iotacc.Hostname()+"/devices/"+dev)),
					statecheck.ExpectKnownValue("echo.tokens", tfjsonpath.New("data").AtMapKey("mod").AtMapKey("resource_uri"), knownvalue.StringExact(iotacc.Hostname()+"/devices/"+dev+"/modules/m1")),
					statecheck.ExpectKnownValue("echo.tokens", tfjsonpath.New("data").AtMapKey("dev").AtMapKey("token"), knownvalue.StringRegexp(regexp.MustCompile(`^SharedAccessSignature sr=.*&sig=.*&se=\d+$`))),
				},
				Check: func(s *terraform.State) error {
					attrs := s.RootModule().Resources["echo.tokens"].Primary.Attributes
					devTok, modTok := attrs["data.dev.token"], attrs["data.mod.token"]
					// a real device-side call proves the token format and signature
					c2d := "/devices/" + dev + "/messages/deviceBound"
					if code := deviceGET(t, c2d, devTok); code != 204 {
						return fmt.Errorf("device token rejected by the device endpoint: HTTP %d", code)
					}
					if code := deviceGET(t, c2d, modTok); code != 401 {
						return fmt.Errorf("module token must not authenticate the device, got HTTP %d", code)
					}
					if !strings.Contains(modTok, "sr="+url.QueryEscape(iotacc.Hostname()+"/devices/"+dev+"/modules/m1")+"&") {
						return fmt.Errorf("module token sr: %s", regexp.MustCompile(`sig=[^&]*`).ReplaceAllString(modTok, "sig=…"))
					}
					exp, err := time.Parse(time.RFC3339, attrs["data.dev.expires_at"])
					if err != nil {
						return err
					}
					if d := time.Until(exp); d > 30*time.Minute || d < 20*time.Minute {
						return fmt.Errorf("expiry %s not ~30m ahead", exp)
					}
					return nil
				},
			},
			{
				Config: iotacc.ProviderConfig() + fmt.Sprintf(`
resource "iothub_device" "x509" {
  device_id = "%s-x509"
  authentication = { type = "certificateAuthority" }
}
ephemeral "iothub_sas_token" "x509" {
  device_id = iothub_device.x509.device_id
}
provider "echo" {
  data = ephemeral.iothub_sas_token.x509
}
resource "echo" "x509" {}
`, dev),
				ExpectError: regexp.MustCompile(`No symmetric key to sign with`),
			},
			{ // clean up the x509 device created by the failed step
				Config: iotacc.ProviderConfig() + fmt.Sprintf(`
resource "iothub_device" "x509" {
  device_id = "%s-x509"
  authentication = { type = "certificateAuthority" }
}`, dev),
			},
		},
	})
}

func TestAccDeviceSASToken_configValidation(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactoriesWithEcho,
		Steps: []resource.TestStep{
			{
				Config: `
ephemeral "iothub_sas_token" "bad" {
  device_id = "x"
  ttl       = "soon"
}
provider "echo" {
  data = ephemeral.iothub_sas_token.bad
}
resource "echo" "bad" {}
`,
				ExpectError: regexp.MustCompile(`Invalid ttl`),
			},
			{
				Config: `
ephemeral "iothub_sas_token" "bad" {
  device_id = "x"
  key       = "tertiary"
}
provider "echo" {
  data = ephemeral.iothub_sas_token.bad
}
resource "echo" "bad" {}
`,
				ExpectError: regexp.MustCompile(`Invalid Attribute Value Match`),
			},
		},
	})
}
