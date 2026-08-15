package provider_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
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
	"github.com/mwalser/terraform-provider-iothub/internal/twinpatch"
)

func TestAccActions_scheduledJobs(t *testing.T) {
	marker := acctest.RandomWithPrefix("tfacc")
	dev := marker + "-dev"
	twinJob, methodJob, futureJob := marker+"-twin", marker+"-method", marker+"-future"
	base := iotacc.ProviderConfig() + fmt.Sprintf(`
resource "iothub_device" "d" {
  device_id = %q
}
resource "iothub_device_twin" "d" {
  device_id = iothub_device.d.device_id
  tags      = jsonencode({ tfacc = %q })
}`, dev, marker)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { iotacc.PreCheck(t) },
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactories,
		CheckDestroy:             iotacc.CheckDeviceDestroyed(dev),
		Steps: []resource.TestStep{
			{Config: base},
			{ // twin update job over the tagged device
				PreConfig: func() {
					waitForQuery(t, fmt.Sprintf("SELECT deviceId FROM devices WHERE tags.tfacc = '%s'", marker), 1)
				},
				Config: base + fmt.Sprintf(`
action "iothub_scheduled_job" "twin" {
  config {
    job_id                     = %q
    type                       = "scheduleUpdateTwin"
    query_condition            = "tags.tfacc = '%s'"
    max_execution_time_seconds = 300
    twin_patch = {
      tags               = jsonencode({ jobbed = true })
      desired_properties = jsonencode({ fromJob = 1 })
    }
  }
}
resource "terraform_data" "twin_job" {
  input = "1"
  lifecycle {
    action_trigger {
      events  = [after_create]
      actions = [action.iothub_scheduled_job.twin]
    }
  }
}`, twinJob, marker),
				Check: func(_ *terraform.State) error {
					tags, desired := twinSections(t, dev)
					if v := tags["jobbed"]; v != true {
						return fmt.Errorf("job did not patch tags: %s", twinpatch.Encode(tags))
					}
					if _, ok := desired["fromJob"]; !ok {
						return fmt.Errorf("job did not patch desired: %s", twinpatch.Encode(desired))
					}
					return nil
				},
			},
			{ // read it back with the data source
				Config: base + fmt.Sprintf(`
data "iothub_scheduled_job" "twin" {
  job_id = %q
}`, twinJob),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("data.iothub_scheduled_job.twin", tfjsonpath.New("status"), knownvalue.StringExact("completed")),
					statecheck.ExpectKnownValue("data.iothub_scheduled_job.twin", tfjsonpath.New("type"), knownvalue.StringExact("scheduleUpdateTwin")),
					statecheck.ExpectKnownValue("data.iothub_scheduled_job.twin", tfjsonpath.New("device_job_statistics").AtMapKey("succeeded_count"), knownvalue.Int64Exact(1)),
					statecheck.ExpectKnownValue("data.iothub_scheduled_job.twin", tfjsonpath.New("device_job_statistics").AtMapKey("failed_count"), knownvalue.Int64Exact(0)),
					statecheck.ExpectKnownValue("data.iothub_scheduled_job.twin", tfjsonpath.New("twin_patch"), knownvalue.StringRegexp(regexp.MustCompile(`"jobbed":true`))),
				},
			},
			{ // a device-method job on an offline device: the job finishes, but with a failed device
				Config: base + fmt.Sprintf(`
action "iothub_scheduled_job" "method" {
  config {
    job_id                     = %q
    type                       = "scheduleDeviceMethod"
    query_condition            = "tags.tfacc = '%s'"
    max_execution_time_seconds = 60
    method = {
      name                     = "reboot"
      response_timeout_seconds = 5
    }
  }
}
resource "terraform_data" "method_job" {
  input = "1"
  lifecycle {
    action_trigger {
      events  = [after_create]
      actions = [action.iothub_scheduled_job.method]
    }
  }
}`, methodJob, marker),
				ExpectError: regexp.MustCompile(`(?s)Scheduled job (completed with device failures|failed)`),
			},
			{ // a future job created without waiting (start_time must be within 168 h)
				Config: base + fmt.Sprintf(`
action "iothub_scheduled_job" "future" {
  config {
    job_id          = %q
    type            = "scheduleUpdateTwin"
    query_condition = "tags.tfacc = '%s'"
    start_time      = %q
    twin_patch      = { tags = jsonencode({ later = true }) }
    wait            = false
  }
}
resource "terraform_data" "future_job" {
  input = "1"
  lifecycle {
    action_trigger {
      events  = [after_create]
      actions = [action.iothub_scheduled_job.future]
    }
  }
}`, futureJob, marker, time.Now().Add(2*time.Hour).UTC().Format(time.RFC3339)),
			},
			{ // …then cancelled (a scheduled job blocks the hub's single job slot until it runs or is cancelled)
				Config: base + fmt.Sprintf(`
action "iothub_cancel_job" "future" {
  config {
    job_id = %q
    kind   = "scheduled"
  }
}
resource "terraform_data" "cancel_job" {
  input = "1"
  lifecycle {
    action_trigger {
      events  = [after_create]
      actions = [action.iothub_cancel_job.future]
    }
  }
}`, futureJob),
			},
			{
				Config: base + fmt.Sprintf(`
data "iothub_scheduled_job" "future" {
  job_id = %q
}`, futureJob),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("data.iothub_scheduled_job.future", tfjsonpath.New("status"), knownvalue.StringExact("cancelled")),
				},
			},
			{
				Config:      iotacc.ProviderConfig() + `data "iothub_scheduled_job" "missing" { job_id = "tf-acc-does-not-exist" }`,
				ExpectError: regexp.MustCompile(`Scheduled job not found`),
			},
		},
	})
}

// TestAccActions_importExportJobs needs a blob container the hub can write
// to (a container SAS URI with rwl permissions) in
// IOTHUB_TEST_BLOB_CONTAINER_SAS_URI; it is skipped otherwise.
func TestAccActions_importExportJobs(t *testing.T) {
	sas := os.Getenv("IOTHUB_TEST_BLOB_CONTAINER_SAS_URI")
	if sas == "" {
		t.Skip("IOTHUB_TEST_BLOB_CONTAINER_SAS_URI not set")
	}
	dev := acctest.RandomWithPrefix("tf-acc")
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { iotacc.PreCheck(t) },
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactories,
		CheckDestroy:             iotacc.CheckDeviceDestroyed(dev),
		Steps: []resource.TestStep{
			{ // export the registry (keys excluded, configurations included)
				Config: iotacc.ProviderConfig() + fmt.Sprintf(`
variable "sas" {
  type      = string
  sensitive = true
  default   = %q
}
resource "iothub_device" "d" {
  device_id = %q
}
action "iothub_import_export_job" "export" {
  config {
    type                      = "export"
    output_blob_container_uri = var.sas
    exclude_keys_in_export    = true
    include_configurations    = true
    output_blob_name          = "tfacc-devices.txt"
    timeout                   = "20m"
  }
}
resource "terraform_data" "export" {
  input = "1"
  lifecycle {
    action_trigger {
      events  = [after_create]
      actions = [action.iothub_import_export_job.export]
    }
  }
}`, sas, dev),
			},
			{ // import: upload a devices.txt (one create line) through the SAS, then let the hub import it
				PreConfig: func() {
					line := fmt.Sprintf(`{"id":%q,"importMode":"create","status":"enabled","authentication":{"type":"sas","symmetricKey":{"primaryKey":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=","secondaryKey":"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBA="}},"tags":{"tfacc":"import"}}`, dev+"-imported")
					if err := uploadBlob(sas, "tfacc-import.txt", line+"\n"); err != nil {
						t.Fatalf("upload: %v", err)
					}
				},
				Config: iotacc.ProviderConfig() + fmt.Sprintf(`
variable "sas" {
  type      = string
  sensitive = true
  default   = %q
}
resource "iothub_device" "d" {
  device_id = %q
}
action "iothub_import_export_job" "import" {
  config {
    type                      = "import"
    input_blob_container_uri  = var.sas
    output_blob_container_uri = var.sas
    input_blob_name           = "tfacc-import.txt"
    timeout                   = "20m"
  }
}
resource "terraform_data" "import" {
  input = "1"
  lifecycle {
    action_trigger {
      events  = [after_create]
      actions = [action.iothub_import_export_job.import]
    }
  }
}`, sas, dev),
				Check: func(_ *terraform.State) error {
					c := iotacc.Client(t)
					got, err := c.GetDevice(context.Background(), dev+"-imported")
					if err != nil {
						return fmt.Errorf("imported device: %w", err)
					}
					if got.Authentication == nil || got.Authentication.SymmetricKey == nil || got.Authentication.SymmetricKey.PrimaryKey != "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" {
						return fmt.Errorf("imported device keys: %+v", got.Authentication)
					}
					return c.DeleteDevice(context.Background(), dev+"-imported", "*")
				},
			},
			{ // a bad SAS fails synchronously and clearly
				Config: iotacc.ProviderConfig() + fmt.Sprintf(`
resource "iothub_device" "d" {
  device_id = %q
}
action "iothub_import_export_job" "bad" {
  config {
    type                      = "export"
    output_blob_container_uri = "https://example.blob.core.windows.net/nope?sv=2022-11-02&sig=invalid"
  }
}
resource "terraform_data" "bad" {
  input = "1"
  lifecycle {
    action_trigger {
      events  = [after_create]
      actions = [action.iothub_import_export_job.bad]
    }
  }
}`, dev),
				ExpectError: regexp.MustCompile(`cannot access the blob container`),
			},
		},
	})
}

// uploadBlob PUTs a block blob into a container through its SAS URI.
func uploadBlob(containerSAS, name, body string) error {
	u, err := url.Parse(containerSAS)
	if err != nil {
		return err
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + "/" + name
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, u.String(), strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("x-ms-blob-type", "BlockBlob")
	req.Header.Set("Content-Type", "text/plain")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("upload %s: HTTP %d", name, resp.StatusCode)
	}
	return nil
}

func TestAccActions_jobConfigValidation(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
action "iothub_scheduled_job" "bad" {
  config {
    type            = "scheduleUpdateTwin"
    query_condition = "*"
    method          = { name = "x" }
  }
}
resource "terraform_data" "t" {
  lifecycle {
    action_trigger {
      events  = [after_create]
      actions = [action.iothub_scheduled_job.bad]
    }
  }
}`,
				ExpectError: regexp.MustCompile(`twin_patch is required`),
			},
			{
				Config: `
action "iothub_scheduled_job" "bad" {
  config {
    type            = "scheduleUpdateTwin"
    query_condition = "*"
    start_time      = "tomorrow"
    twin_patch      = { tags = jsonencode({ a = 1 }) }
  }
}
resource "terraform_data" "t" {
  lifecycle {
    action_trigger {
      events  = [after_create]
      actions = [action.iothub_scheduled_job.bad]
    }
  }
}`,
				ExpectError: regexp.MustCompile(`Invalid start_time`),
			},
			{
				Config: `
action "iothub_import_export_job" "bad" {
  config {
    type                      = "import"
    output_blob_container_uri = "https://x"
  }
}
resource "terraform_data" "t" {
  lifecycle {
    action_trigger {
      events  = [after_create]
      actions = [action.iothub_import_export_job.bad]
    }
  }
}`,
				ExpectError: regexp.MustCompile(`input_blob_container_uri is required for imports`),
			},
			{
				Config: `
action "iothub_cancel_job" "bad" {
  config {
    job_id = "x"
    kind   = "nope"
  }
}
resource "terraform_data" "t" {
  lifecycle {
    action_trigger {
      events  = [after_create]
      actions = [action.iothub_cancel_job.bad]
    }
  }
}`,
				ExpectError: regexp.MustCompile(`Invalid Attribute Value Match`),
			},
		},
	})
}
