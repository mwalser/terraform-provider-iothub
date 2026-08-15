// Package acctest holds the shared harness for acceptance tests, which run
// against a real IoT Hub (the F1 free tier is sufficient). Configuration is
// taken from the environment:
//
//	TF_ACC=1                              enable acceptance tests
//	IOTHUB_TEST_HOSTNAME=<hub>.azure-devices.net
//	credentials for one auth mode: an `az login` session / ARM_* variables
//	(Entra ID) or IOTHUB_CONNECTION_STRING (SAS)
package acctest

import (
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"

	"github.com/mwalser/terraform-provider-iothub/internal/provider"
)

// HostnameEnv names the environment variable with the test hub.
const HostnameEnv = "IOTHUB_TEST_HOSTNAME"

// ProtoV6ProviderFactories instantiates the provider for terraform-plugin-testing.
var ProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"iothub": providerserver.NewProtocol6WithError(provider.New("test")()),
}

// PreCheck skips the test unless the acceptance environment is complete.
func PreCheck(t *testing.T) {
	t.Helper()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("TF_ACC not set; skipping acceptance test")
	}
	if Hostname() == "" {
		t.Fatalf("%s must be set for acceptance tests", HostnameEnv)
	}
}

// Hostname returns the test hub.
func Hostname() string { return os.Getenv(HostnameEnv) }

// UsingSAS reports whether the acceptance run authenticates with a
// connection string rather than Entra ID.
func UsingSAS() bool { return os.Getenv("IOTHUB_CONNECTION_STRING") != "" }

// ProviderConfig returns a provider block for test configurations. The
// hostname is passed explicitly so a stray IOTHUB_HOSTNAME cannot redirect
// tests to another hub; credentials come from the environment.
func ProviderConfig() string {
	return fmt.Sprintf(`
provider "iothub" {
  hostname = %q
}
`, Hostname())
}
