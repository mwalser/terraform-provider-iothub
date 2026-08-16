package provider

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"software.sslmate.com/src/go-pkcs12"

	"github.com/mwalser/terraform-provider-iothub/internal/provider/common"
)

// testCertificate returns a self-signed certificate and its RSA key (Entra ID
// client certificates must be RSA).
func testCertificate(t *testing.T) (*x509.Certificate, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "tf-test"}, NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key
}

func TestParseClientCertificate_ModernPKCS12AndPEM(t *testing.T) {
	cert, key := testCertificate(t)
	// OpenSSL 3 defaults: AES-256-CBC + SHA-256 MAC — what azurerm's guide produces.
	pfx, err := pkcs12.Modern.Encode(key, cert, nil, "pw")
	if err != nil {
		t.Fatal(err)
	}
	certs, gotKey, err := parseClientCertificate(pfx, "pw")
	if err != nil {
		t.Fatalf("modern PKCS#12 must parse: %v", err)
	}
	if len(certs) != 1 || certs[0].Subject.CommonName != "tf-test" || gotKey == nil {
		t.Fatalf("PKCS#12 contents: %d certs, key %v", len(certs), gotKey != nil)
	}
	if _, _, err := parseClientCertificate(pfx, "wrong"); err == nil {
		t.Error("wrong password must fail")
	}
	// legacy PKCS#12 (SHA-1 MAC, 3DES) keeps working
	legacy, _ := pkcs12.Legacy.Encode(key, cert, nil, "pw")
	if _, _, err := parseClientCertificate(legacy, "pw"); err != nil {
		t.Errorf("legacy PKCS#12: %v", err)
	}
	// PEM bundle with an unencrypted PKCS#8 key
	pk8, _ := x509.MarshalPKCS8PrivateKey(key)
	pemData := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk8})...)
	certs, gotKey, err = parseClientCertificate(pemData, "")
	if err != nil || len(certs) != 1 || gotKey == nil {
		t.Fatalf("PEM: %v %d", err, len(certs))
	}
	// and through the credential builder, base64 inline
	_, err = newEntraCredential(common.EntraSettings{TenantID: "t", ClientID: "c", ClientCertificate: base64.StdEncoding.EncodeToString(pfx), ClientCertificatePassword: "pw"})
	if err != nil {
		t.Errorf("client_certificate (base64 PKCS#12): %v", err)
	}
}

func TestNewEntraCredential_Selection(t *testing.T) {
	// nothing configured and the CLI disabled → a clear error, no guessing
	if _, err := newEntraCredential(common.EntraSettings{}); err == nil || !strings.Contains(err.Error(), "no Entra ID authentication method") {
		t.Fatalf("expected the no-method error, got %v", err)
	}
	// use_cli (the default) alone works without any other input
	if _, err := newEntraCredential(common.EntraSettings{UseCLI: true}); err != nil {
		t.Fatalf("CLI: %v", err)
	}
	// a client secret needs tenant and client IDs
	if _, err := newEntraCredential(common.EntraSettings{ClientSecret: "s", UseCLI: true}); err == nil || !strings.Contains(err.Error(), "tenant_id and client_id") {
		t.Fatalf("expected the missing-IDs error, got %v", err)
	}
	if _, err := newEntraCredential(common.EntraSettings{ClientSecret: "s", TenantID: "t", ClientID: "c", UseCLI: true}); err != nil {
		t.Fatalf("client secret: %v", err)
	}
	// use_oidc without any token source
	if _, err := newEntraCredential(common.EntraSettings{UseOIDC: true, TenantID: "t", ClientID: "c"}); err == nil || !strings.Contains(err.Error(), "no federated token") {
		t.Fatalf("expected the no-token error, got %v", err)
	}
	// Azure DevOps needs the request URL and the system token
	if _, err := newEntraCredential(common.EntraSettings{UseOIDC: true, TenantID: "t", ClientID: "c", ADOPipelineServiceConnID: "sc"}); err == nil || !strings.Contains(err.Error(), "SYSTEM_OIDCREQUESTURI") {
		t.Fatalf("expected the Azure DevOps inputs error, got %v", err)
	}
	// use_msi
	if _, err := newEntraCredential(common.EntraSettings{UseMSI: true, ClientID: "c"}); err != nil {
		t.Fatalf("MSI: %v", err)
	}
}

func TestOIDCAssertion_Sources(t *testing.T) {
	ctx := context.Background()
	get, err := oidcAssertion(common.EntraSettings{OIDCToken: "literal"})
	if err != nil {
		t.Fatal(err)
	}
	if tok, _ := get(ctx); tok != "literal" {
		t.Errorf("token = %q", tok)
	}

	dir := t.TempDir()
	file := filepath.Join(dir, "token")
	if err := os.WriteFile(file, []byte("from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	get, err = oidcAssertion(common.EntraSettings{OIDCTokenFilePath: file})
	if err != nil {
		t.Fatal(err)
	}
	if tok, _ := get(ctx); tok != "from-file" {
		t.Errorf("token from file = %q", tok)
	}
	// the file is read on every request (rotating tokens)
	_ = os.WriteFile(file, []byte("rotated"), 0o600)
	if tok, _ := get(ctx); tok != "rotated" {
		t.Errorf("rotated token = %q", tok)
	}

	// GitHub Actions request endpoint
	var gotAuth, gotAudience string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotAudience = r.Header.Get("Authorization"), r.URL.Query().Get("audience")
		_ = json.NewEncoder(w).Encode(map[string]string{"value": "gh-jwt"})
	}))
	defer srv.Close()
	get, err = oidcAssertion(common.EntraSettings{OIDCRequestURL: srv.URL + "/token?x=1", OIDCRequestToken: "req"})
	if err != nil {
		t.Fatal(err)
	}
	tok, err := get(ctx)
	if err != nil || tok != "gh-jwt" {
		t.Fatalf("github token = %q %v", tok, err)
	}
	if gotAuth != "bearer req" || gotAudience != oidcAudience {
		t.Errorf("request: auth %q audience %q", gotAuth, gotAudience)
	}

	// Azure DevOps service connection: oidc_request_url is used, not the environment
	var gotMethod, gotQuery, gotADOAuth string
	ado := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotQuery, gotADOAuth = r.Method, r.URL.RawQuery, r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]string{"oidcToken": "ado-jwt"})
	}))
	defer ado.Close()
	get, err = oidcAssertion(common.EntraSettings{ADOPipelineServiceConnID: "sc-1", OIDCRequestURL: ado.URL + "/_apis/distributedtask/hubs/build/plans/p/jobs/j/oidctoken", OIDCRequestToken: "sys"})
	if err != nil {
		t.Fatal(err)
	}
	tok, err = get(ctx)
	if err != nil || tok != "ado-jwt" {
		t.Fatalf("ado token = %q %v", tok, err)
	}
	if gotMethod != http.MethodPost || !strings.Contains(gotQuery, "api-version=7.1") || !strings.Contains(gotQuery, "serviceConnectionId=sc-1") || gotADOAuth != "Bearer sys" {
		t.Errorf("ado request: %s %q auth %q", gotMethod, gotQuery, gotADOAuth)
	}
}
