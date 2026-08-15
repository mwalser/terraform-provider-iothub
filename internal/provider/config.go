package provider

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/mwalser/terraform-provider-iothub/internal/provider/common"
)

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

// resolve merges configuration and environment (configuration wins),
// infers the auth mode and validates the result.
func resolve(cfg rawConfig, env envLookup) (common.Settings, error) {
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

	s := common.Settings{
		Hostname: strings.TrimSpace(first(cfg.Hostname, "IOTHUB_HOSTNAME")),
	}

	if cs := first(cfg.ConnectionString, "IOTHUB_CONNECTION_STRING"); cs != "" {
		cred, err := parseConnectionString(cs)
		if err != nil {
			return common.Settings{}, err
		}
		s.Mode = common.AuthSAS
		s.SAS = cred
		switch {
		case s.Hostname == "":
			s.Hostname = strings.ToLower(cred.HostName)
		case !strings.EqualFold(s.Hostname, cred.HostName):
			return common.Settings{}, fmt.Errorf("hostname %q does not match the HostName in connection_string (%q)", s.Hostname, cred.HostName)
		}
	} else {
		s.Mode = common.AuthEntraID
		var err error
		s.Entra = common.EntraSettings{
			TenantID:                  first(cfg.TenantID, "ARM_TENANT_ID", "AZURE_TENANT_ID"),
			ClientID:                  first(cfg.ClientID, "ARM_CLIENT_ID", "AZURE_CLIENT_ID"),
			ClientSecret:              first(cfg.ClientSecret, "ARM_CLIENT_SECRET", "AZURE_CLIENT_SECRET"),
			ClientCertificatePath:     first(cfg.ClientCertificatePath, "ARM_CLIENT_CERTIFICATE_PATH", "AZURE_CLIENT_CERTIFICATE_PATH"),
			ClientCertificatePassword: first(cfg.ClientCertificatePassword, "ARM_CLIENT_CERTIFICATE_PASSWORD", "AZURE_CLIENT_CERTIFICATE_PASSWORD"),
			OIDCTokenFilePath:         first(cfg.OIDCTokenFilePath, "ARM_OIDC_TOKEN_FILE_PATH", "AZURE_FEDERATED_TOKEN_FILE"),
		}
		if s.Entra.UseOIDC, err = firstBool(cfg.UseOIDC, "ARM_USE_OIDC"); err != nil {
			return common.Settings{}, err
		}
		if s.Entra.UseMSI, err = firstBool(cfg.UseMSI, "ARM_USE_MSI"); err != nil {
			return common.Settings{}, err
		}
		if s.Entra.UseCLI, err = firstBool(cfg.UseCLI, "ARM_USE_CLI"); err != nil {
			return common.Settings{}, err
		}
	}

	if s.Hostname != "" {
		if err := common.ValidateHostname(s.Hostname); err != nil {
			return common.Settings{}, err
		}
	}
	return s, nil
}

// parseConnectionString parses an IoT Hub *service* connection string.
func parseConnectionString(cs string) (*common.SASCredential, error) {
	cred := &common.SASCredential{}
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
	// The HostName of a connection string is Azure-generated; its case is
	// irrelevant (the client lowercases it) so only the shape is checked.
	if err := common.ValidateHostname(strings.ToLower(cred.HostName)); err != nil {
		return nil, fmt.Errorf("connection_string: %w", err)
	}
	return cred, nil
}
