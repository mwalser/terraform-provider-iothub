package provider

import (
	"strings"
	"testing"

	"github.com/mwalser/terraform-provider-iothub/internal/provider/common"
)

func envFrom(m map[string]string) envLookup {
	return func(k string) string { return m[k] }
}

func TestResolve_EntraDefaults(t *testing.T) {
	s, err := resolve(rawConfig{Hostname: "contoso.azure-devices.net"}, envFrom(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Mode != common.AuthEntraID {
		t.Fatalf("mode = %v, want entra-id", s.Mode)
	}
	if s.Hostname != "contoso.azure-devices.net" {
		t.Fatalf("hostname = %q", s.Hostname)
	}
	if s.SAS != nil {
		t.Fatalf("SAS credential must be nil in Entra mode")
	}
}

func TestResolve_EnvFallbackAndPrecedence(t *testing.T) {
	env := envFrom(map[string]string{
		"IOTHUB_HOSTNAME": "env.azure-devices.net",
		"ARM_TENANT_ID":   "t-arm", "AZURE_TENANT_ID": "t-azure",
		"AZURE_CLIENT_ID":            "c-azure",
		"ARM_USE_OIDC":               "true",
		"AZURE_FEDERATED_TOKEN_FILE": "/var/run/secrets/token",
	})
	s, err := resolve(rawConfig{ClientID: "c-explicit"}, env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Hostname != "env.azure-devices.net" {
		t.Errorf("hostname from env: got %q", s.Hostname)
	}
	if s.Entra.TenantID != "t-arm" {
		t.Errorf("ARM_* must win over AZURE_*: got %q", s.Entra.TenantID)
	}
	if s.Entra.ClientID != "c-explicit" {
		t.Errorf("explicit config must win over env: got %q", s.Entra.ClientID)
	}
	if !s.Entra.UseOIDC || s.Entra.OIDCTokenFilePath != "/var/run/secrets/token" {
		t.Errorf("oidc settings not resolved: %+v", s.Entra)
	}
}

func TestResolve_BadBoolEnv(t *testing.T) {
	_, err := resolve(rawConfig{}, envFrom(map[string]string{"ARM_USE_MSI": "maybe"}))
	if err == nil || !strings.Contains(err.Error(), "ARM_USE_MSI") {
		t.Fatalf("expected ARM_USE_MSI parse error, got %v", err)
	}
}

func TestResolve_ConnectionString(t *testing.T) {
	cs := "HostName=contoso.azure-devices.net;SharedAccessKeyName=iothubowner;SharedAccessKey=c2VjcmV0"
	s, err := resolve(rawConfig{ConnectionString: cs}, envFrom(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Mode != common.AuthSAS || s.SAS == nil {
		t.Fatalf("expected SAS mode with credential, got %+v", s)
	}
	if s.Hostname != "contoso.azure-devices.net" {
		t.Errorf("hostname must be derived from the connection string, got %q", s.Hostname)
	}
	if s.SAS.SharedAccessKeyName != "iothubowner" || s.SAS.SharedAccessKey != "c2VjcmV0" {
		t.Errorf("credential parsed wrongly: %+v", s.SAS)
	}
	// Explicit hostname that disagrees with the connection string is an error.
	_, err = resolve(rawConfig{Hostname: "other.azure-devices.net", ConnectionString: cs}, envFrom(nil))
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Errorf("expected mismatch error, got %v", err)
	}
	// The connection string's HostName may be spelled with capitals (Azure-generated); the explicit hostname must be lowercase.
	if _, err = resolve(rawConfig{Hostname: "contoso.azure-devices.net", ConnectionString: strings.Replace(cs, "contoso.azure-devices.net", "Contoso.Azure-Devices.NET", 1)}, envFrom(nil)); err != nil {
		t.Errorf("case-insensitive hostname match should pass: %v", err)
	}
	if _, err = resolve(rawConfig{Hostname: "Contoso.Azure-Devices.NET", ConnectionString: cs}, envFrom(nil)); err == nil || !strings.Contains(err.Error(), "lowercase") {
		t.Errorf("mixed-case explicit hostname must be rejected, got %v", err)
	}
	// Env var works too.
	s, err = resolve(rawConfig{}, envFrom(map[string]string{"IOTHUB_CONNECTION_STRING": cs}))
	if err != nil || s.Mode != common.AuthSAS {
		t.Errorf("IOTHUB_CONNECTION_STRING not honoured: %v %+v", err, s)
	}
}

func TestParseConnectionString_Errors(t *testing.T) {
	cases := map[string]string{
		"missing key":      "HostName=contoso.azure-devices.net;SharedAccessKeyName=iothubowner",
		"malformed":        "HostName=contoso.azure-devices.net;garbage",
		"device string":    "HostName=contoso.azure-devices.net;DeviceId=d1;SharedAccessKey=abc",
		"non public cloud": "HostName=contoso.azure-devices.us;SharedAccessKeyName=iothubowner;SharedAccessKey=abc",
	}
	for name, cs := range cases {
		if _, err := parseConnectionString(cs); err == nil {
			t.Errorf("%s: expected error for %q", name, cs)
		}
	}
}

func TestValidateHostname(t *testing.T) {
	good := []string{"contoso.azure-devices.net", "contoso-prod.azure-devices.net"}
	bad := []string{"contoso", "https://contoso.azure-devices.net", "contoso.azure-devices.net/", ".azure-devices.net", "contoso.azure-devices.us", "contoso.azure-devices.cn",
		"Contoso-Prod.AZURE-DEVICES.NET", " contoso.azure-devices.net"}
	for _, h := range good {
		if err := common.ValidateHostname(h); err != nil {
			t.Errorf("%q should be valid: %v", h, err)
		}
	}
	for _, h := range bad {
		if err := common.ValidateHostname(h); err == nil {
			t.Errorf("%q should be invalid", h)
		}
	}
	// a connection string's HostName is Azure-generated: case is tolerated and normalised
	cred, err := parseConnectionString("HostName=Contoso.Azure-Devices.NET;SharedAccessKeyName=iothubowner;SharedAccessKey=a2V5")
	if err != nil {
		t.Fatalf("mixed-case connection string HostName should parse: %v", err)
	}
	s, err := resolve(rawConfig{ConnectionString: "HostName=Contoso.Azure-Devices.NET;SharedAccessKeyName=iothubowner;SharedAccessKey=a2V5"}, envFrom(nil))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if s.Hostname != "contoso.azure-devices.net" || cred.HostName != "Contoso.Azure-Devices.NET" {
		t.Errorf("default hostname from the connection string must be lowercase, got %q", s.Hostname)
	}
	if _, err := resolve(rawConfig{Hostname: "Contoso.azure-devices.net"}, envFrom(nil)); err == nil {
		t.Error("a mixed-case provider hostname must be rejected")
	}
}
