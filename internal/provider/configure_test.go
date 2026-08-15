package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// configure runs Provider.Configure with the given attribute values; values
// omitted from attrs are null, tftypes.UnknownValue marks unknowns.
func configure(t *testing.T, attrs map[string]tftypes.Value) provider.ConfigureResponse {
	t.Helper()
	ctx := context.Background()
	p := New("test")()

	var sr provider.SchemaResponse
	p.Schema(ctx, provider.SchemaRequest{}, &sr)

	objType, ok := sr.Schema.Type().TerraformType(ctx).(tftypes.Object)
	if !ok {
		t.Fatalf("provider schema type is %T, want tftypes.Object", sr.Schema.Type().TerraformType(ctx))
	}
	vals := map[string]tftypes.Value{}
	for name, at := range objType.AttributeTypes {
		if v, ok := attrs[name]; ok {
			vals[name] = v
		} else {
			vals[name] = tftypes.NewValue(at, nil)
		}
	}
	req := provider.ConfigureRequest{Config: tfsdk.Config{Schema: sr.Schema, Raw: tftypes.NewValue(objType, vals)}}
	var resp provider.ConfigureResponse
	p.Configure(ctx, req, &resp)
	return resp
}

func str(s string) tftypes.Value { return tftypes.NewValue(tftypes.String, s) }

// providerData extracts the *ProviderData handed to resources.
func providerData(t *testing.T, resp provider.ConfigureResponse) *ProviderData {
	t.Helper()
	pd, ok := resp.ResourceData.(*ProviderData)
	if !ok {
		t.Fatalf("ResourceData is %T, want *ProviderData", resp.ResourceData)
	}
	return pd
}

// isolateEnv clears every environment variable the provider reads so the
// developer's shell cannot influence the tests.
func isolateEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"IOTHUB_HOSTNAME", "IOTHUB_CONNECTION_STRING",
		"ARM_TENANT_ID", "AZURE_TENANT_ID", "ARM_CLIENT_ID", "AZURE_CLIENT_ID",
		"ARM_CLIENT_SECRET", "AZURE_CLIENT_SECRET", "ARM_CLIENT_CERTIFICATE_PATH", "AZURE_CLIENT_CERTIFICATE_PATH",
		"ARM_CLIENT_CERTIFICATE_PASSWORD", "AZURE_CLIENT_CERTIFICATE_PASSWORD",
		"ARM_USE_OIDC", "ARM_OIDC_TOKEN_FILE_PATH", "AZURE_FEDERATED_TOKEN_FILE", "ARM_USE_MSI", "ARM_USE_CLI",
	} {
		t.Setenv(k, "")
	}
}

func TestConfigure_EntraDefault(t *testing.T) {
	isolateEnv(t)
	resp := configure(t, map[string]tftypes.Value{"hostname": str("contoso.azure-devices.net")})
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
	pd := providerData(t, resp)
	if pd.Settings.Mode != AuthEntraID || pd.Settings.Hostname != "contoso.azure-devices.net" || pd.HostnameUnknown {
		t.Errorf("unexpected settings: %+v", pd)
	}
	if resp.DataSourceData == nil || resp.EphemeralResourceData == nil || resp.ActionData == nil {
		t.Errorf("provider data must be handed to every construct kind")
	}
}

func TestConfigure_SAS(t *testing.T) {
	isolateEnv(t)
	resp := configure(t, map[string]tftypes.Value{
		"connection_string": str("HostName=x.azure-devices.net;SharedAccessKeyName=iothubowner;SharedAccessKey=YWJj"),
	})
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
	pd := providerData(t, resp)
	if pd.Settings.Mode != AuthSAS || pd.Settings.Hostname != "x.azure-devices.net" || pd.Settings.SAS.SharedAccessKeyName != "iothubowner" {
		t.Errorf("unexpected settings: %+v", pd.Settings)
	}
}

func TestConfigure_InvalidHostname(t *testing.T) {
	isolateEnv(t)
	resp := configure(t, map[string]tftypes.Value{"hostname": str("contoso.azure-devices.us")})
	if !resp.Diagnostics.HasError() {
		t.Fatalf("expected an error for a non-public-cloud hostname")
	}
	if !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "public cloud") {
		t.Errorf("unexpected error text: %s", resp.Diagnostics.Errors()[0].Detail())
	}
}

func TestConfigure_UnknownValues(t *testing.T) {
	isolateEnv(t)
	// hostname may be unknown (it typically references azurerm_iothub.x.hostname).
	resp := configure(t, map[string]tftypes.Value{"hostname": tftypes.NewValue(tftypes.String, tftypes.UnknownValue)})
	if resp.Diagnostics.HasError() {
		t.Fatalf("unknown hostname must be tolerated: %v", resp.Diagnostics)
	}
	if pd := providerData(t, resp); !pd.HostnameUnknown || pd.Settings.Hostname != "" {
		t.Errorf("expected HostnameUnknown with empty hostname, got %+v", pd)
	}
	// anything needed for authentication may not be unknown.
	resp = configure(t, map[string]tftypes.Value{"client_secret": tftypes.NewValue(tftypes.String, tftypes.UnknownValue)})
	if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "client_secret") {
		t.Fatalf("expected an error naming client_secret, got %v", resp.Diagnostics)
	}
}
