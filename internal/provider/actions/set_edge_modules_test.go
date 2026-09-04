package actions

import (
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/mwalser/terraform-provider-iothub/internal/provider/providertest"
)

// TestSetEdgeModules_UnresolvedPlaceholder: a placeholder without a value
// is never applied to a device, even when validation could not see the
// document at plan time.
func TestSetEdgeModules_UnresolvedPlaceholder(t *testing.T) {
	ctx := t.Context()
	var calls atomic.Int32
	var body atomic.Value
	a := &setEdgeModulesAction{}
	a.pd = providertest.HubWith(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		b, _ := io.ReadAll(r.Body)
		body.Store(string(b))
		w.WriteHeader(http.StatusNoContent)
	}))
	var sr action.SchemaResponse
	a.Schema(ctx, action.SchemaRequest{}, &sr)
	objType, ok := sr.Schema.Type().TerraformType(ctx).(tftypes.Object)
	if !ok {
		t.Fatalf("schema type %T", sr.Schema.Type().TerraformType(ctx))
	}
	config := func(secrets map[string]tftypes.Value) tfsdk.Config {
		vals := map[string]tftypes.Value{
			"device_id":       tftypes.NewValue(tftypes.String, "edge-1"),
			"modules_content": tftypes.NewValue(tftypes.String, `{"$edgeAgent":{"properties.desired.modules.m":{"env":{"K":{"value":"{{secret.k}}"}}}}}`),
			"secrets":         tftypes.NewValue(tftypes.Map{ElementType: tftypes.String}, secrets),
			"timeout":         tftypes.NewValue(tftypes.String, nil),
		}
		return tfsdk.Config{Schema: sr.Schema, Raw: tftypes.NewValue(objType, vals)}
	}
	var resp action.InvokeResponse
	a.Invoke(ctx, action.InvokeRequest{Config: config(map[string]tftypes.Value{"other": tftypes.NewValue(tftypes.String, "v")})}, &resp)
	if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics[0].Detail(), "`{{secret.k}}` at $edgeAgent.properties.desired.modules.m.env.K.value has no value") || calls.Load() != 0 {
		t.Fatalf("unresolved placeholder: %v, hub calls %d", resp.Diagnostics, calls.Load())
	}
	resp = action.InvokeResponse{}
	a.Invoke(ctx, action.InvokeRequest{Config: config(map[string]tftypes.Value{"k": tftypes.NewValue(tftypes.String, "v")})}, &resp)
	sent, _ := body.Load().(string)
	if resp.Diagnostics.HasError() || calls.Load() != 1 || !strings.Contains(sent, `"value":"v"`) || strings.Contains(sent, "{{secret") {
		t.Fatalf("resolved placeholder: %v, hub calls %d, body %s", resp.Diagnostics, calls.Load(), sent)
	}
}
