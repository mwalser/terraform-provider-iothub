package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/mwalser/terraform-provider-iothub/internal/provider/common"
)

// newEntraCredential picks the Entra ID credential the same way azurerm
// does: the first configured method wins, in the order client certificate,
// client secret, OIDC (use_oidc), managed identity (use_msi), Azure CLI
// (use_cli, on by default). Nothing is probed or guessed: a method is used
// only when its inputs are present, so a wrong secret fails instead of
// silently falling back to a developer's CLI login.
func newEntraCredential(s common.EntraSettings) (azcore.TokenCredential, error) {
	needIDs := func(method string) error {
		if s.TenantID == "" || s.ClientID == "" {
			return fmt.Errorf("%s authentication needs tenant_id and client_id", method)
		}
		return nil
	}
	switch {
	case s.ClientCertificatePath != "" || s.ClientCertificate != "":
		if err := needIDs("client certificate"); err != nil {
			return nil, err
		}
		var data []byte
		var err error
		if s.ClientCertificatePath != "" {
			if data, err = os.ReadFile(s.ClientCertificatePath); err != nil {
				return nil, fmt.Errorf("reading client certificate: %w", err)
			}
		} else if data, err = base64.StdEncoding.DecodeString(s.ClientCertificate); err != nil {
			return nil, fmt.Errorf("client_certificate must be base64: %w", err)
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

	case s.ClientSecret != "":
		if err := needIDs("client_secret"); err != nil {
			return nil, err
		}
		return azidentity.NewClientSecretCredential(s.TenantID, s.ClientID, s.ClientSecret, nil)

	case s.UseOIDC:
		if err := needIDs("OIDC"); err != nil {
			return nil, err
		}
		if s.ADOPipelineServiceConnID != "" {
			if s.OIDCRequestToken == "" {
				return nil, fmt.Errorf("OIDC authentication through Azure DevOps needs the pipeline's system access token (oidc_request_token or SYSTEM_ACCESSTOKEN)")
			}
			return azidentity.NewAzurePipelinesCredential(s.TenantID, s.ClientID, s.ADOPipelineServiceConnID, s.OIDCRequestToken, nil)
		}
		getAssertion, err := oidcAssertion(s)
		if err != nil {
			return nil, err
		}
		return azidentity.NewClientAssertionCredential(s.TenantID, s.ClientID, getAssertion, nil)

	case s.UseMSI:
		opts := &azidentity.ManagedIdentityCredentialOptions{}
		if s.ClientID != "" {
			opts.ID = azidentity.ClientID(s.ClientID)
		}
		return azidentity.NewManagedIdentityCredential(opts)

	case s.UseCLI:
		return azidentity.NewAzureCLICredential(&azidentity.AzureCLICredentialOptions{TenantID: s.TenantID})

	default:
		return nil, fmt.Errorf("no Entra ID authentication method is configured: set client_secret or a client certificate, use_oidc, use_msi, or use_cli (or set connection_string for SAS authentication)")
	}
}

// oidcAssertion returns the federated token source for use_oidc: a literal
// token, a token file (read on every request, because such files rotate),
// or the GitHub Actions token request endpoint.
func oidcAssertion(s common.EntraSettings) (func(context.Context) (string, error), error) {
	switch {
	case s.OIDCToken != "":
		return func(context.Context) (string, error) { return s.OIDCToken, nil }, nil
	case s.OIDCTokenFilePath != "":
		return func(context.Context) (string, error) {
			b, err := os.ReadFile(s.OIDCTokenFilePath)
			if err != nil {
				return "", fmt.Errorf("reading oidc_token_file_path: %w", err)
			}
			return strings.TrimSpace(string(b)), nil
		}, nil
	case s.OIDCRequestURL != "" && s.OIDCRequestToken != "":
		return func(ctx context.Context) (string, error) {
			return requestGitHubOIDCToken(ctx, s.OIDCRequestURL, s.OIDCRequestToken)
		}, nil
	default:
		return nil, fmt.Errorf("use_oidc is set but no federated token is available: set oidc_token, oidc_token_file_path, or oidc_request_url and oidc_request_token (GitHub Actions provides them as ACTIONS_ID_TOKEN_REQUEST_URL and ACTIONS_ID_TOKEN_REQUEST_TOKEN when the job has `id-token: write` permission)")
	}
}

// oidcAudience is the audience Entra ID expects in a federated token.
const oidcAudience = "api://AzureADTokenExchange"

// requestGitHubOIDCToken fetches an ID token from the GitHub Actions token
// endpoint, as azurerm does.
func requestGitHubOIDCToken(ctx context.Context, requestURL, requestToken string) (string, error) {
	u, err := url.Parse(requestURL)
	if err != nil {
		return "", fmt.Errorf("oidc_request_url: %w", err)
	}
	q := u.Query()
	q.Set("audience", oidcAudience)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "bearer "+requestToken)
	req.Header.Set("Accept", "application/json; api-version=2.0")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("requesting the OIDC token: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("requesting the OIDC token: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.Value == "" {
		return "", fmt.Errorf("requesting the OIDC token: unexpected response %q", strings.TrimSpace(string(body)))
	}
	return out.Value, nil
}
