// Package providertest supports unit tests of the provider's resources with
// a fake hub, where the acceptance tests need a real one.
package providertest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/common"
)

// Str is a known string value for state attributes.
func Str(s string) tftypes.Value { return tftypes.NewValue(tftypes.String, s) }

// GoneHub returns provider data for a hub that answers every request with
// 404, as it does for an object deleted outside Terraform.
func GoneHub(t *testing.T) *common.ProviderData {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("iothub-errorcode", "DeviceNotFound")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"Message":"ErrorCode:DeviceNotFound;not found","ExceptionMessage":"Tracking ID:test"}`))
	}))
	t.Cleanup(srv.Close)
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	c, err := client.New(client.Config{
		Hostname:   "unit.azure-devices.net",
		Credential: fakeCred{},
		Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
			req.URL.Scheme, req.URL.Host = target.Scheme, target.Host
			return srv.Client().Do(req)
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	return &common.ProviderData{Client: c}
}

type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) Do(req *http.Request) (*http.Response, error) { return f(req) }

// ReadGone reads r against GoneHub the way a Terraform core without resource
// identity support (before 1.12) does: with the prior state attrs (attributes
// not listed are null) and no current identity. The read must remove the
// state without an error and still return the identity want (attribute name
// to value), because the framework rejects a read that returns none
// ("Missing Resource Identity After Read").
func ReadGone(t *testing.T, r resource.Resource, attrs map[string]tftypes.Value, want map[string]string) {
	t.Helper()
	ctx := t.Context()
	if rc, ok := r.(resource.ResourceWithConfigure); ok {
		var cr resource.ConfigureResponse
		rc.Configure(ctx, resource.ConfigureRequest{ProviderData: GoneHub(t)}, &cr)
		if cr.Diagnostics.HasError() {
			t.Fatalf("configure: %v", cr.Diagnostics)
		}
	}
	var sr resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sr)
	objType, ok := sr.Schema.Type().TerraformType(ctx).(tftypes.Object)
	if !ok {
		t.Fatalf("resource schema type is %T, want tftypes.Object", sr.Schema.Type().TerraformType(ctx))
	}
	vals := make(map[string]tftypes.Value, len(objType.AttributeTypes))
	for name, at := range objType.AttributeTypes {
		if v, ok := attrs[name]; ok {
			vals[name] = v
		} else {
			vals[name] = tftypes.NewValue(at, nil)
		}
	}
	for name := range attrs {
		if _, ok := objType.AttributeTypes[name]; !ok {
			t.Fatalf("attribute %q is not in the schema", name)
		}
	}
	ri, ok := r.(resource.ResourceWithIdentity)
	if !ok {
		t.Fatalf("%T has no resource identity", r)
	}
	var ir resource.IdentitySchemaResponse
	ri.IdentitySchema(ctx, resource.IdentitySchemaRequest{}, &ir)
	nullID := tftypes.NewValue(ir.IdentitySchema.Type().TerraformType(ctx), nil)
	state := tftypes.NewValue(objType, vals)
	req := resource.ReadRequest{
		State:    tfsdk.State{Schema: sr.Schema, Raw: state},
		Identity: &tfsdk.ResourceIdentity{Schema: ir.IdentitySchema, Raw: nullID.Copy()},
	}
	resp := resource.ReadResponse{
		State:    tfsdk.State{Schema: sr.Schema, Raw: state.Copy()},
		Identity: &tfsdk.ResourceIdentity{Schema: ir.IdentitySchema, Raw: nullID.Copy()},
	}
	r.Read(ctx, req, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("read: %v", resp.Diagnostics)
	}
	if !resp.State.Raw.IsNull() {
		t.Error("state kept for an object the hub no longer has")
	}
	if resp.Identity.Raw.IsFullyNull() {
		t.Fatal(`no identity returned; the framework fails such a read with "Missing Resource Identity After Read"`)
	}
	for name, v := range want {
		var got types.String
		if diags := resp.Identity.GetAttribute(ctx, path.Root(name), &got); diags.HasError() {
			t.Fatalf("identity %s: %v", name, diags)
		}
		if got.ValueString() != v {
			t.Errorf("identity %s = %q, want %q", name, got.ValueString(), v)
		}
	}
}
