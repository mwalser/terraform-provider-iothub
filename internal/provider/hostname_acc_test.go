package provider_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	iotacc "github.com/mwalser/terraform-provider-iothub/internal/acctest"
)

// Every `hostname` attribute demands the canonical lowercase spelling:
// Terraform compares strings exactly and a provider cannot normalise a
// configured value, so a mixed-case hostname would otherwise end in
// "inconsistent result" errors or spurious replacements. Rejected up front,
// on every construct kind, before any hub is contacted.
func TestAccHostname_mustBeLowercase(t *testing.T) {
	lowercase := regexp.MustCompile(`must be lowercase`)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{ // resource
				Config: `resource "iothub_device" "d" {
  hostname  = "Contoso.azure-devices.net"
  device_id = "d1"
}`,
				ExpectError: lowercase,
			},
			{ // data source
				Config: `data "iothub_digital_twin" "d" {
  hostname        = "contoso.Azure-Devices.NET"
  digital_twin_id = "d1"
}`,
				ExpectError: lowercase,
			},
			{ // ephemeral resource
				Config: `ephemeral "iothub_device_sas_token" "t" {
  hostname  = "Contoso.azure-devices.net"
  device_id = "d1"
}`,
				ExpectError: lowercase,
			},
			{ // action
				Config: `action "iothub_purge_c2d_queue" "p" {
  config {
    hostname  = "Contoso.azure-devices.net"
    device_id = "d1"
  }
}
resource "terraform_data" "t" {
  lifecycle {
    action_trigger {
      events  = [after_create]
      actions = [action.iothub_purge_c2d_queue.p]
    }
  }
}`,
				ExpectError: lowercase,
			},
			{ // provider block (spelled with capitals; under SAS it must still name the connection string's hub)
				Config: fmt.Sprintf(`provider "iothub" {
  hostname = %q
}
data "iothub_statistics" "s" {}`, strings.ToUpper(iotacc.Hostname()[:1])+iotacc.Hostname()[1:]),
				ExpectError: lowercase,
			},
			{ // other shape errors are still reported
				Config: `resource "iothub_device" "d" {
  hostname  = "https://contoso.azure-devices.net"
  device_id = "d1"
}`,
				ExpectError: regexp.MustCompile(`not a URL`),
			},
			{
				Config: `resource "iothub_device" "d" {
  hostname  = "contoso.azure-devices.us"
  device_id = "d1"
}`,
				ExpectError: regexp.MustCompile(`must end in \.azure-devices\.net`),
			},
		},
	})
}
