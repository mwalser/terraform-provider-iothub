package provider_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	iotacc "github.com/mwalser/terraform-provider-iothub/internal/acctest"
)

// The provider's `hostname` demands the canonical lowercase spelling:
// Terraform compares strings exactly and a provider cannot normalise a
// configured value, so a mixed-case hostname would otherwise end in
// "inconsistent result" errors or spurious replacements. Rejected up front,
// before any hub is contacted.
func TestAccHostname_mustBeLowercase(t *testing.T) {
	// Under SAS the provider block must name the connection string's hub;
	// without a test hub configured (plain unit run) any name will do.
	hub := iotacc.Hostname()
	if hub == "" {
		hub = "contoso.azure-devices.net"
	}
	withHub := func(h string) string {
		return fmt.Sprintf("provider \"iothub\" {\n  hostname = %q\n}\ndata \"iothub_statistics\" \"s\" {}\n", h)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      withHub(strings.ToUpper(hub[:1]) + hub[1:]),
				ExpectError: regexp.MustCompile(`must be lowercase`),
			},
			{
				Config:      withHub("contoso.Azure-Devices.NET"),
				ExpectError: regexp.MustCompile(`must be lowercase`),
			},
			{ // other shape errors are still reported
				Config:      withHub("https://contoso.azure-devices.net"),
				ExpectError: regexp.MustCompile(`not a URL`),
			},
			{
				Config:      withHub("contoso.azure-devices.us"),
				ExpectError: regexp.MustCompile(`must end in \.azure-devices\.net`),
			},
		},
	})
}
