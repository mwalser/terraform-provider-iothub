package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mwalser/terraform-provider-iothub/internal/provider/common"
)

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
}
