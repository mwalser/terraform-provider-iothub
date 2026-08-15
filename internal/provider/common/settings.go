// Package common holds what every resource, data source, ephemeral resource
// and action of the provider shares: the resolved provider settings, the
// client factory and hostname resolution.
package common

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
