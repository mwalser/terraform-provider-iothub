package provider

import (
	"fmt"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/mwalser/terraform-provider-iothub/internal/provider/common"
)

// newEntraCredential builds the azidentity credential for the resolved
// settings. Explicit selections win in this order: client secret, client
// certificate, OIDC/workload identity, managed identity, Azure CLI; with
// nothing selected the azidentity default chain (environment → workload
// identity → managed identity → Azure CLI → Azure Developer CLI) is used.
func newEntraCredential(s common.EntraSettings) (azcore.TokenCredential, error) {
	switch {
	case s.ClientSecret != "":
		if s.TenantID == "" || s.ClientID == "" {
			return nil, fmt.Errorf("client_secret authentication needs tenant_id and client_id")
		}
		return azidentity.NewClientSecretCredential(s.TenantID, s.ClientID, s.ClientSecret, nil)

	case s.ClientCertificatePath != "":
		if s.TenantID == "" || s.ClientID == "" {
			return nil, fmt.Errorf("client_certificate_path authentication needs tenant_id and client_id")
		}
		data, err := os.ReadFile(s.ClientCertificatePath)
		if err != nil {
			return nil, fmt.Errorf("reading client certificate: %w", err)
		}
		var password []byte
		if s.ClientCertificatePassword != "" {
			password = []byte(s.ClientCertificatePassword)
		}
		certs, key, err := azidentity.ParseCertificates(data, password)
		if err != nil {
			return nil, fmt.Errorf("parsing client certificate: %w", err)
		}
		return azidentity.NewClientCertificateCredential(s.TenantID, s.ClientID, certs, key, nil)

	case s.UseOIDC:
		return azidentity.NewWorkloadIdentityCredential(&azidentity.WorkloadIdentityCredentialOptions{
			TenantID:      s.TenantID,
			ClientID:      s.ClientID,
			TokenFilePath: s.OIDCTokenFilePath,
		})

	case s.UseMSI:
		opts := &azidentity.ManagedIdentityCredentialOptions{}
		if s.ClientID != "" {
			opts.ID = azidentity.ClientID(s.ClientID)
		}
		return azidentity.NewManagedIdentityCredential(opts)

	case s.UseCLI:
		return azidentity.NewAzureCLICredential(&azidentity.AzureCLICredentialOptions{TenantID: s.TenantID})

	default:
		return azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{TenantID: s.TenantID})
	}
}
