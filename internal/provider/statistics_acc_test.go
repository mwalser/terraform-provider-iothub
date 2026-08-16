package provider_test

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/mwalser/terraform-provider-iothub/internal/acctest"
)

func TestAccStatisticsDataSource_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: acctest.ProviderConfig() + `
data "iothub_statistics" "hub" {}
`,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("data.iothub_statistics.hub", tfjsonpath.New("total_device_count"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue("data.iothub_statistics.hub", tfjsonpath.New("enabled_device_count"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue("data.iothub_statistics.hub", tfjsonpath.New("disabled_device_count"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue("data.iothub_statistics.hub", tfjsonpath.New("connected_device_count"), knownvalue.NotNull()),
				},
			},
		},
	})
}

func TestAccStatisticsDataSource_noHostname(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				// Provider without hostname: a clear error at configure time.
				// Only meaningful under Entra ID; with a connection string the
				// hostname is derived from it.
				SkipFunc:    func() (bool, error) { return acctest.UsingSAS(), nil },
				Config:      `provider "iothub" {}` + "\n" + `data "iothub_statistics" "none" {}`,
				ExpectError: regexp.MustCompile(`No IoT Hub hostname configured`),
			},
		},
	})
}
