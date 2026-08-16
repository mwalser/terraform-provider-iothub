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
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/echoprovider"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
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
// tests to another hub; credentials come from the environment. Unit runs
// without a test hub get a placeholder, which is never contacted.
func ProviderConfig() string {
	hub := Hostname()
	if hub == "" {
		hub = "contoso.azure-devices.net"
	}
	return fmt.Sprintf(`
provider "iothub" {
  hostname = %q
}
`, hub)
}

// ProtoV6ProviderFactoriesWithEcho adds HashiCorp's echo provider, which
// copies ephemeral values into state so tests can assert on them.
var ProtoV6ProviderFactoriesWithEcho = map[string]func() (tfprotov6.ProviderServer, error){
	"iothub": providerserver.NewProtocol6WithError(provider.New("test")()),
	"echo":   echoprovider.NewProviderServer(),
}

// NewClient returns a service client for the test hub using the same
// credentials the provider under test uses (Entra ID via the azidentity
// default chain, or IOTHUB_CONNECTION_STRING).
func NewClient() (*client.Client, error) {
	cfg := client.Config{Hostname: Hostname(), Version: "acctest"}
	if cs := os.Getenv("IOTHUB_CONNECTION_STRING"); cs != "" {
		parts := map[string]string{}
		for _, p := range strings.Split(cs, ";") {
			if k, v, ok := strings.Cut(p, "="); ok {
				parts[k] = v
			}
		}
		cfg.SharedAccessKey = &client.SharedAccessKey{HostName: parts["HostName"], KeyName: parts["SharedAccessKeyName"], Key: parts["SharedAccessKey"]}
	} else {
		cred, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			return nil, fmt.Errorf("credential: %w", err)
		}
		cfg.Credential = cred
	}
	return client.New(cfg)
}

// Client is NewClient for tests: failures end the test.
func Client(t *testing.T) *client.Client {
	t.Helper()
	c, err := NewClient()
	if err != nil {
		t.Fatalf("test hub client: %v", err)
	}
	return c
}

// CheckDeviceDestroyed verifies that the given devices no longer exist.
func CheckDeviceDestroyed(ids ...string) func(*terraform.State) error {
	return func(_ *terraform.State) error {
		if os.Getenv("TF_ACC") == "" {
			return nil
		}
		c, err := NewClient()
		if err != nil {
			return err
		}
		for _, id := range ids {
			_, err := c.GetDevice(context.Background(), id)
			if err == nil {
				return fmt.Errorf("device %q still exists after destroy", id)
			}
			if !client.IsNotFound(err) {
				return fmt.Errorf("checking device %q: %w", id, err)
			}
		}
		return nil
	}
}
