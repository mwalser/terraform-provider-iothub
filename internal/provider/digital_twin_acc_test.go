package provider_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	iotacc "github.com/mwalser/terraform-provider-iothub/internal/acctest"
)

// The digital twin endpoint answers for every device (verified): a device
// that never announced a model has an empty $model.
func TestAccDigitalTwin_dataSource(t *testing.T) {
	dev := acctest.RandomWithPrefix("tf-acc")
	base := iotacc.ProviderConfig() + fmt.Sprintf(`
resource "iothub_device" "d" {
  device_id = %q
}
`, dev)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { iotacc.PreCheck(t) },
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactories,
		CheckDestroy:             iotacc.CheckDeviceDestroyed(dev),
		Steps: []resource.TestStep{
			{
				Config: base + `
data "iothub_digital_twin" "d" {
  device_id = iothub_device.d.device_id
}`,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("data.iothub_digital_twin.d", tfjsonpath.New("id"), knownvalue.StringExact(dev)),
					statecheck.ExpectKnownValue("data.iothub_digital_twin.d", tfjsonpath.New("model_id"), knownvalue.Null()),
					statecheck.ExpectKnownValue("data.iothub_digital_twin.d", tfjsonpath.New("document"), knownvalue.StringRegexp(regexp.MustCompile(`"\$dtId":\s*"`+regexp.QuoteMeta(dev)+`"`))),
					statecheck.ExpectKnownValue("data.iothub_digital_twin.d", tfjsonpath.New("document"), knownvalue.StringRegexp(regexp.MustCompile(`"\$model":\s*""`))),
					statecheck.ExpectKnownValue("data.iothub_digital_twin.d", tfjsonpath.New("etag"), knownvalue.NotNull()),
				},
			},
			{ // a missing device is reported as such
				Config: base + `
data "iothub_digital_twin" "missing" {
  device_id = "does-not-exist-tf-acc"
}`,
				ExpectError: regexp.MustCompile(`Digital twin not found`),
			},
		},
	})
}
