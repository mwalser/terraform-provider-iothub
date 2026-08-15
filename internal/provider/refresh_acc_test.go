package provider_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	iotacc "github.com/mwalser/terraform-provider-iothub/internal/acctest"
	"github.com/mwalser/terraform-provider-iothub/internal/client"
)

// Refresh reads the twin first and skips the registry read while the twin's
// deviceEtag equals the ETag in state (CONCEPT.md §11.2). Every identity
// write moves that ETag, so out-of-band changes must still surface as drift
// — for devices and modules alike. (Every other test's post-apply plan
// exercises the cheap path itself.)
func TestAccRefresh_outOfBandChangesStillDetected(t *testing.T) {
	dev := acctest.RandomWithPrefix("tf-acc")
	cfg := iotacc.ProviderConfig() + fmt.Sprintf(`
resource "iothub_device" "d" {
  device_id     = %q
  status        = "enabled"
  status_reason = "managed by terraform"
}
resource "iothub_module" "m" {
  device_id  = iothub_device.d.device_id
  module_id  = "m1"
  managed_by = "terraform"
}`, dev)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { iotacc.PreCheck(t) },
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactories,
		CheckDestroy:             iotacc.CheckDeviceDestroyed(dev),
		Steps: []resource.TestStep{
			{Config: cfg},
			{ // unchanged: the twin-first refresh must reproduce the state exactly
				Config:   cfg,
				PlanOnly: true,
			},
			{ // status and managedBy changed outside Terraform → the plan reverts both
				PreConfig: func() {
					ctx := context.Background()
					c := iotacc.Client(t)
					d, err := c.GetDevice(ctx, dev)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := c.UpdateDevice(ctx, client.DeviceSpec{
						DeviceID: dev, Status: client.StatusDisabled, StatusReason: "changed out of band", Authentication: *d.Authentication,
					}, d.ETag); err != nil {
						t.Fatalf("out-of-band device update: %v", err)
					}
					m, err := c.GetModule(ctx, dev, "m1")
					if err != nil {
						t.Fatal(err)
					}
					if _, err := c.UpdateModule(ctx, client.ModuleSpec{
						DeviceID: dev, ModuleID: "m1", ManagedBy: "someone else", Authentication: *m.Authentication,
					}, m.ETag); err != nil {
						t.Fatalf("out-of-band module update: %v", err)
					}
				},
				Config: cfg,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("iothub_device.d", plancheck.ResourceActionUpdate),
					plancheck.ExpectResourceAction("iothub_module.m", plancheck.ResourceActionUpdate),
				}},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("iothub_device.d", tfjsonpath.New("status"), knownvalue.StringExact("enabled")),
					statecheck.ExpectKnownValue("iothub_device.d", tfjsonpath.New("status_reason"), knownvalue.StringExact("managed by terraform")),
					statecheck.ExpectKnownValue("iothub_module.m", tfjsonpath.New("managed_by"), knownvalue.StringExact("terraform")),
				},
			},
			{ // module deleted outside Terraform: the twin answers 404 → registry read → gone → re-created
				PreConfig: func() {
					if err := iotacc.Client(t).DeleteModule(context.Background(), dev, "m1", "*"); err != nil {
						t.Fatalf("out-of-band delete: %v", err)
					}
				},
				Config:           cfg,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("iothub_module.m", plancheck.ResourceActionCreate)}},
			},
		},
	})
}
