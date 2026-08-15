package provider

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// AuthMode selects how the provider authenticates against the IoT Hub
// service API. It is inferred from the configuration, never chosen
// explicitly: a connection string selects SAS, anything else Entra ID.
type AuthMode int

const (
	// AuthEntraID uses a Microsoft Entra ID token (scope
	// https://iothubs.azure.net/.default) obtained via the azidentity chain.
	AuthEntraID AuthMode = iota
	// AuthSAS uses a shared access policy of the hub, minting SAS tokens
	// from the policy key.
	AuthSAS
)

func (m AuthMode) String() string {
	if m == AuthSAS {
		return "sas"
	}
	return "entra-id"
}

// EntraSettings are the inputs for the Entra ID credential chain. Empty
// fields fall back to the azidentity defaults (environment, workload
// identity, managed identity, Azure CLI).
type EntraSettings struct {
	TenantID                  string
	ClientID                  string
	ClientSecret              string
	ClientCertificatePath     string
	ClientCertificatePassword string
	UseOIDC                   bool
	OIDCTokenFilePath         string
	UseMSI                    bool
	UseCLI                    bool
}

// SASCredential is a parsed IoT Hub connection string
// (HostName=…;SharedAccessKeyName=…;SharedAccessKey=…).
type SASCredential struct {
	HostName            string
	SharedAccessKeyName string
	SharedAccessKey     string
}

// Settings is the fully resolved provider configuration: explicit
// configuration merged with environment variables and validated.
type Settings struct {
	// Hostname is the default hub (e.g. contoso.azure-devices.net); may be
	// empty when every resource sets its own hostname.
	Hostname string
	Mode     AuthMode
	Entra    EntraSettings
	SAS      *SASCredential
}

// rawConfig mirrors the provider schema before environment fallback.
type rawConfig struct {
	Hostname                  string
	TenantID                  string
	ClientID                  string
	ClientSecret              string
	ClientCertificatePath     string
	ClientCertificatePassword string
	UseOIDC                   *bool
	OIDCTokenFilePath         string
	UseMSI                    *bool
	UseCLI                    *bool
	ConnectionString          string
}

// envLookup abstracts os.Getenv for tests.
type envLookup func(string) string

// publicCloudSuffix is the only hub domain the provider supports (§2.3 /
// §15 row 7 of the concept: public cloud only).
const publicCloudSuffix = ".azure-devices.net"

// resolve merges configuration and environment (configuration wins),
// infers the auth mode and validates the result.
func resolve(cfg rawConfig, env envLookup) (Settings, error) {
	if env == nil {
		env = os.Getenv
	}
	first := func(explicit string, keys ...string) string {
		if explicit != "" {
			return explicit
		}
		for _, k := range keys {
			if v := env(k); v != "" {
				return v
			}
		}
		return ""
	}
	firstBool := func(explicit *bool, keys ...string) (bool, error) {
		if explicit != nil {
			return *explicit, nil
		}
		for _, k := range keys {
			if v := env(k); v != "" {
				b, err := strconv.ParseBool(v)
				if err != nil {
					return false, fmt.Errorf("environment variable %s: %w", k, err)
				}
				return b, nil
			}
		}
		return false, nil
	}

	s := Settings{
		Hostname: strings.TrimSpace(first(cfg.Hostname, "IOTHUB_HOSTNAME")),
	}

	if cs := first(cfg.ConnectionString, "IOTHUB_CONNECTION_STRING"); cs != "" {
		cred, err := parseConnectionString(cs)
		if err != nil {
			return Settings{}, err
		}
		s.Mode = AuthSAS
		s.SAS = cred
		switch {
		case s.Hostname == "":
			s.Hostname = cred.HostName
		case !strings.EqualFold(s.Hostname, cred.HostName):
			return Settings{}, fmt.Errorf("hostname %q does not match the HostName in connection_string (%q)", s.Hostname, cred.HostName)
		}
	} else {
		s.Mode = AuthEntraID
		var err error
		s.Entra = EntraSettings{
			TenantID:                  first(cfg.TenantID, "ARM_TENANT_ID", "AZURE_TENANT_ID"),
			ClientID:                  first(cfg.ClientID, "ARM_CLIENT_ID", "AZURE_CLIENT_ID"),
			ClientSecret:              first(cfg.ClientSecret, "ARM_CLIENT_SECRET", "AZURE_CLIENT_SECRET"),
			ClientCertificatePath:     first(cfg.ClientCertificatePath, "ARM_CLIENT_CERTIFICATE_PATH", "AZURE_CLIENT_CERTIFICATE_PATH"),
			ClientCertificatePassword: first(cfg.ClientCertificatePassword, "ARM_CLIENT_CERTIFICATE_PASSWORD", "AZURE_CLIENT_CERTIFICATE_PASSWORD"),
			OIDCTokenFilePath:         first(cfg.OIDCTokenFilePath, "ARM_OIDC_TOKEN_FILE_PATH", "AZURE_FEDERATED_TOKEN_FILE"),
		}
		if s.Entra.UseOIDC, err = firstBool(cfg.UseOIDC, "ARM_USE_OIDC"); err != nil {
			return Settings{}, err
		}
		if s.Entra.UseMSI, err = firstBool(cfg.UseMSI, "ARM_USE_MSI"); err != nil {
			return Settings{}, err
		}
		if s.Entra.UseCLI, err = firstBool(cfg.UseCLI, "ARM_USE_CLI"); err != nil {
			return Settings{}, err
		}
	}

	if s.Hostname != "" {
		if err := validateHostname(s.Hostname); err != nil {
			return Settings{}, err
		}
	}
	return s, nil
}

// validateHostname accepts fully-qualified public-cloud IoT Hub hostnames.
func validateHostname(h string) error {
	lower := strings.ToLower(h)
	if strings.Contains(lower, "://") || strings.Contains(lower, "/") {
		return fmt.Errorf("hostname %q must be a bare host name such as contoso.azure-devices.net, not a URL", h)
	}
	if !strings.HasSuffix(lower, publicCloudSuffix) || len(lower) == len(publicCloudSuffix) {
		return fmt.Errorf("hostname %q must end in %s (only the Azure public cloud is supported)", h, publicCloudSuffix)
	}
	return nil
}

// parseConnectionString parses an IoT Hub *service* connection string.
func parseConnectionString(cs string) (*SASCredential, error) {
	cred := &SASCredential{}
	for _, part := range strings.Split(strings.TrimSpace(cs), ";") {
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			return nil, fmt.Errorf("connection_string: malformed segment %q", part)
		}
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "hostname":
			cred.HostName = strings.TrimSpace(v)
		case "sharedaccesskeyname":
			cred.SharedAccessKeyName = strings.TrimSpace(v)
		case "sharedaccesskey":
			cred.SharedAccessKey = strings.TrimSpace(v)
		case "deviceid", "moduleid":
			return nil, fmt.Errorf("connection_string: this looks like a device or module connection string; the provider needs a hub shared access policy (HostName=…;SharedAccessKeyName=…;SharedAccessKey=…)")
		}
	}
	var missing []string
	if cred.HostName == "" {
		missing = append(missing, "HostName")
	}
	if cred.SharedAccessKeyName == "" {
		missing = append(missing, "SharedAccessKeyName")
	}
	if cred.SharedAccessKey == "" {
		missing = append(missing, "SharedAccessKey")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("connection_string: missing %s", strings.Join(missing, ", "))
	}
	if err := validateHostname(cred.HostName); err != nil {
		return nil, fmt.Errorf("connection_string: %w", err)
	}
	return cred, nil
}
