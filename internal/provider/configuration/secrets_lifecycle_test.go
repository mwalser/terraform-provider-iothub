package configuration

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/jsondoc"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/providertest"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/secrets"
	"github.com/mwalser/terraform-provider-iothub/internal/twinpatch"
)

// The lifecycle of an edge deployment with secret placeholders against a
// fake hub (CONCEPT.md §6.7), one framework call at a time: what the
// acceptance test verifies live, pinned where a unit test can.

// edgeSchema returns the edge deployment resource's schema and its
// Terraform object type.
func edgeSchema(t *testing.T) (schema.Schema, tftypes.Object) {
	t.Helper()
	var sr resource.SchemaResponse
	(&configResource{kind: edgeDeploymentKind}).Schema(t.Context(), resource.SchemaRequest{}, &sr)
	objType, ok := sr.Schema.Type().TerraformType(t.Context()).(tftypes.Object)
	if !ok {
		t.Fatalf("schema type %T", sr.Schema.Type().TerraformType(t.Context()))
	}
	return sr.Schema, objType
}

// edgeRaw builds a raw edge deployment value from attrs; every other
// attribute is null.
func edgeRaw(t *testing.T, objType tftypes.Object, attrs map[string]tftypes.Value) tftypes.Value {
	t.Helper()
	vals := make(map[string]tftypes.Value, len(objType.AttributeTypes))
	for name, typ := range objType.AttributeTypes {
		vals[name] = tftypes.NewValue(typ, nil)
	}
	for name, v := range attrs {
		if _, ok := objType.AttributeTypes[name]; !ok {
			t.Fatalf("attribute %q is not in the schema", name)
		}
		vals[name] = v
	}
	return tftypes.NewValue(objType, vals)
}

// rawSecrets renders a secrets_wo map; "" as a value stands for unknown.
func rawSecrets(m map[string]string) tftypes.Value {
	elems := make(map[string]tftypes.Value, len(m))
	for k, v := range m {
		if v == "" {
			elems[k] = tftypes.NewValue(tftypes.String, tftypes.UnknownValue)
		} else {
			elems[k] = tftypes.NewValue(tftypes.String, v)
		}
	}
	return tftypes.NewValue(tftypes.Map{ElementType: tftypes.String}, elems)
}

// deployment is the state of dep-1 with placeholders in modules_content
// and a fingerprint of the hub's values.
func deployment(t *testing.T, content string, fingerprint tftypes.Value) map[string]tftypes.Value {
	t.Helper()
	return map[string]tftypes.Value{
		"id": providertest.Str("dep-1"), "deployment_id": providertest.Str("dep-1"), "target_condition": providertest.Str("*"),
		"priority": tftypes.NewValue(tftypes.Number, 10), "modules_content": providertest.Str(content),
		"secrets_fingerprint": fingerprint, "etag": providertest.Str("MQ=="),
	}
}

// proposed is the plan Terraform proposes for an in-place update of
// deployment attrs: the computed attributes it does not know until apply
// are unknown.
func proposed(t *testing.T, objType tftypes.Object, attrs map[string]tftypes.Value) tftypes.Value {
	t.Helper()
	plan := make(map[string]tftypes.Value, len(attrs))
	for k, v := range attrs {
		plan[k] = v
	}
	for _, name := range []string{"secrets_fingerprint", "etag", "created_time", "last_updated_time", "system_metrics", "metric_results"} {
		plan[name] = tftypes.NewValue(objType.AttributeTypes[name], tftypes.UnknownValue)
	}
	return edgeRaw(t, objType, plan)
}

// noRequests is a hub that must not be asked anything.
func noRequests(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		http.Error(w, "unexpected", http.StatusBadRequest)
	})
}

// TestModifyPlan_Secrets: the plan reads the write-only values from the
// configuration, compares them with the state's fingerprint and asks the
// hub nothing; a difference is blamed on secrets_fingerprint, which the
// plan shows as unknown.
func TestModifyPlan_Secrets(t *testing.T) {
	ctx := t.Context()
	r := &configResource{kind: edgeDeploymentKind, pd: providertest.HubWith(t, noRequests(t))}
	sch, objType := edgeSchema(t)
	fp := fingerprintOf(t, tokenized, resolved(values))
	attrs := deployment(t, tokenized, providertest.Str(fp.ValueString()))
	cases := []struct {
		name        string
		secrets     map[string]string
		wantReplace bool
		wantWarning bool
	}{
		{"steady state", values, false, false},
		{"rotated", map[string]string{"acr": "new", "conn": values["conn"]}, true, false},
		{"unknown value", map[string]string{"acr": "", "conn": values["conn"]}, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			config := map[string]tftypes.Value{"deployment_id": attrs["deployment_id"], "target_condition": attrs["target_condition"],
				"priority": attrs["priority"], "modules_content": attrs["modules_content"], "secrets_wo": rawSecrets(c.secrets)}
			state := edgeRaw(t, objType, attrs)
			req := resource.ModifyPlanRequest{
				Config: tfsdk.Config{Schema: sch, Raw: edgeRaw(t, objType, config)},
				Plan:   tfsdk.Plan{Schema: sch, Raw: state.Copy()}, // secrets_wo is not in the plan: nothing differs from state
				State:  tfsdk.State{Schema: sch, Raw: state},
			}
			resp := resource.ModifyPlanResponse{Plan: tfsdk.Plan{Schema: sch, Raw: state.Copy()}}
			r.ModifyPlan(ctx, req, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatal(resp.Diagnostics)
			}
			if resp.Deferred != nil {
				t.Error("deferred")
			}
			m, diags := edgeDeploymentKind.get(ctx, resp.Plan)
			if diags.HasError() {
				t.Fatal(diags)
			}
			replaced := resp.RequiresReplace.Contains(path.Root("secrets_fingerprint"))
			if replaced != c.wantReplace || len(resp.RequiresReplace) != map[bool]int{true: 1, false: 0}[c.wantReplace] {
				t.Errorf("requires replace: %v, want on secrets_fingerprint: %v", resp.RequiresReplace, c.wantReplace)
			}
			if m.SecretsFingerprint.IsUnknown() != c.wantReplace {
				t.Errorf("secrets_fingerprint in the plan: %s", m.SecretsFingerprint)
			}
			if !c.wantReplace && !m.SecretsFingerprint.Equal(fp) {
				t.Errorf("an unchanged deployment keeps its fingerprint: %s", m.SecretsFingerprint)
			}
			if warned := resp.Diagnostics.WarningsCount() > 0; warned != c.wantWarning {
				t.Errorf("warnings: %v, want %v", resp.Diagnostics, c.wantWarning)
			}
			if raw := resp.Plan.Raw.String(); strings.Contains(raw, values["acr"]) || strings.Contains(raw, "new") || !strings.Contains(raw, "{{secret.acr}}") {
				t.Errorf("a value reached the plan: %s", raw)
			}
		})
	}
}

// readDeployment refreshes state against a hub that returns hub as the
// deployment's modulesContent.
func readDeployment(t *testing.T, state tftypes.Value, hub string) resource.ReadResponse {
	t.Helper()
	sch, _ := edgeSchema(t)
	r := &configResource{kind: edgeDeploymentKind}
	r.pd = providertest.HubWith(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(client.Configuration{ID: "dep-1", TargetCondition: "*", Priority: 10, ETag: "MQ==",
			Content: client.ConfigurationContent{ModulesContent: json.RawMessage(hub)}})
	}))
	resp := resource.ReadResponse{State: tfsdk.State{Schema: sch, Raw: state.Copy()}}
	r.Read(t.Context(), resource.ReadRequest{State: tfsdk.State{Schema: sch, Raw: state}}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	return resp
}

// TestRead_FollowsTheHub: a deployment recreated outside Terraform with
// another value. The refresh keeps the placeholders, and the fingerprint
// follows the hub under the salt state holds, so the next plan sees the
// change without the value ever entering state.
func TestRead_FollowsTheHub(t *testing.T) {
	_, objType := edgeSchema(t)
	fp := fingerprintOf(t, tokenized, resolved(values))
	drifted := map[string]string{"acr": values["acr"], "conn": "Endpoint=other"}
	resp := readDeployment(t, edgeRaw(t, objType, deployment(t, tokenized, providertest.Str(fp.ValueString()))), resolved(drifted))
	if raw := resp.State.Raw.String(); strings.Contains(raw, "Endpoint=other") || strings.Contains(raw, values["acr"]) {
		t.Fatalf("a value reached state: %s", raw)
	}
	m, diags := edgeDeploymentKind.get(t.Context(), resp.State)
	if diags.HasError() {
		t.Fatal(diags)
	}
	if m.ModulesContent.ValueString() != tokenized {
		t.Errorf("state must keep the placeholders: %s", m.ModulesContent)
	}
	// Under the recorded salt the fingerprint of the drifted values is one
	// string; a fresh salt would give another.
	want := fingerprint(jsondoc.NewValue(ModulesContentType, tokenized), fp, []byte(resolved(drifted)))
	if !m.SecretsFingerprint.Equal(want) {
		t.Errorf("the fingerprint must follow the hub under the same salt: %s, want %s", m.SecretsFingerprint, want)
	}
	prior, _ := twinpatch.Decode(tokenized)
	if secrets.ParseFingerprint(m.SecretsFingerprint.ValueString()).Matches(secrets.Refs(prior), values) {
		t.Error("the configured values must no longer match")
	}
}

// updateDeployment applies plan over state with the configured secrets
// against hub.
func updateDeployment(t *testing.T, hub http.Handler, state, plan tftypes.Value, configured map[string]string) resource.UpdateResponse {
	t.Helper()
	sch, objType := edgeSchema(t)
	r := &configResource{kind: edgeDeploymentKind, pd: providertest.HubWith(t, hub)}
	config := map[string]tftypes.Value{"secrets_wo": rawSecrets(configured)}
	for _, name := range []string{"deployment_id", "target_condition", "priority", "modules_content"} {
		var v map[string]tftypes.Value
		if err := plan.As(&v); err != nil {
			t.Fatal(err)
		}
		config[name] = v[name]
	}
	req := resource.UpdateRequest{
		Config: tfsdk.Config{Schema: sch, Raw: edgeRaw(t, objType, config)},
		Plan:   tfsdk.Plan{Schema: sch, Raw: plan},
		State:  tfsdk.State{Schema: sch, Raw: state},
	}
	resp := resource.UpdateResponse{State: tfsdk.State{Schema: sch, Raw: plan.Copy()}}
	r.Update(t.Context(), req, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	if !resp.State.Raw.IsFullyKnown() {
		t.Errorf("state not fully known after apply: %s", resp.State.Raw)
	}
	return resp
}

// TestUpdate_StateOnly: placeholders taking over an imported document —
// the hub's values in state — are recorded without a write. The hub gets
// no request, the ETag stays, the fingerprint is taken with a fresh salt,
// and the values never enter state.
func TestUpdate_StateOnly(t *testing.T) {
	_, objType := edgeSchema(t)
	null := tftypes.NewValue(tftypes.String, nil)
	state := edgeRaw(t, objType, deployment(t, resolved(values), null))
	plan := proposed(t, objType, deployment(t, tokenized, null))
	resp := updateDeployment(t, noRequests(t), state, plan, values)
	if raw := resp.State.Raw.String(); strings.Contains(raw, values["acr"]) || strings.Contains(raw, values["conn"]) {
		t.Fatalf("a value reached state: %s", raw)
	}
	m, diags := edgeDeploymentKind.get(t.Context(), resp.State)
	if diags.HasError() {
		t.Fatal(diags)
	}
	if m.ModulesContent.ValueString() != tokenized || m.ETag.ValueString() != "MQ==" {
		t.Errorf("state: content %s, etag %s", m.ModulesContent, m.ETag)
	}
	prior, _ := twinpatch.Decode(tokenized)
	if m.SecretsFingerprint.IsNull() || !secrets.ParseFingerprint(m.SecretsFingerprint.ValueString()).Matches(secrets.Refs(prior), values) {
		t.Errorf("fingerprint of the adopted values: %s", m.SecretsFingerprint)
	}
}

// TestUpdate_WritesResolved: a changed priority writes the document with
// the values filled in under the state's ETag; what the hub returns is
// stored with the placeholders, and the fingerprint keeps its salt.
func TestUpdate_WritesResolved(t *testing.T) {
	_, objType := edgeSchema(t)
	fp := fingerprintOf(t, tokenized, resolved(values))
	var written string
	hub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/configurations/dep-1" || !strings.Contains(r.Header.Get("If-Match"), "MQ==") {
			t.Errorf("unexpected request %s %s If-Match %q", r.Method, r.URL.Path, r.Header.Get("If-Match"))
		}
		var spec client.Configuration
		if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
			t.Error(err)
		}
		written = string(spec.Content.ModulesContent)
		spec.ETag = "Mg=="
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(spec) // the hub echoes the manifest
	})
	state := edgeRaw(t, objType, deployment(t, tokenized, providertest.Str(fp.ValueString())))
	attrs := deployment(t, tokenized, providertest.Str(fp.ValueString()))
	attrs["priority"] = tftypes.NewValue(tftypes.Number, 11)
	resp := updateDeployment(t, hub, state, proposed(t, objType, attrs), values)
	if !jsondoc.SemanticallyEqual(written, resolved(values)) {
		t.Errorf("the hub must receive the resolved document: %s", written)
	}
	if raw := resp.State.Raw.String(); strings.Contains(raw, values["acr"]) || strings.Contains(raw, values["conn"]) {
		t.Fatalf("a value reached state: %s", raw)
	}
	m, diags := edgeDeploymentKind.get(t.Context(), resp.State)
	if diags.HasError() {
		t.Fatal(diags)
	}
	if m.ModulesContent.ValueString() != tokenized || m.ETag.ValueString() != "Mg==" || m.Priority.ValueInt64() != 11 {
		t.Errorf("state: content %s, etag %s, priority %d", m.ModulesContent, m.ETag, m.Priority.ValueInt64())
	}
	if !m.SecretsFingerprint.Equal(fp) {
		t.Errorf("unchanged values under the same salt keep the fingerprint: %s -> %s", fp, m.SecretsFingerprint)
	}
}
