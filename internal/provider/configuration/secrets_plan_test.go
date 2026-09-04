package configuration

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/mwalser/terraform-provider-iothub/internal/client"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/jsondoc"
	"github.com/mwalser/terraform-provider-iothub/internal/provider/secrets"
	"github.com/mwalser/terraform-provider-iothub/internal/twinpatch"
)

const tokenized = `{"$edgeAgent":{"properties.desired":{"modules":{"m":{"env":{"CONN":{"value":"{{secret.conn}}"}}}},"runtime":{"settings":{"registryCredentials":{"acr":{"address":"a","password":"{{secret.acr}}","username":"u"}}}}}},"$edgeHub":{"properties.desired":{"routes":{}}}}`

var values = map[string]string{"acr": "p4ss", "conn": "Endpoint=x"}

// resolved is the tokenized document with values filled in, as the hub
// holds it.
func resolved(values map[string]string) string {
	s, _ := secrets.ResolveJSON(tokenized, values) // every token has a value
	return s
}

func edgeModel(content string) model {
	return model{ConfigurationID: types.StringValue("dep-1"), ModulesContent: jsondoc.NewValue(ModulesContentType, content)}
}

// fingerprintOf returns the secrets_fingerprint a refresh of hub would
// leave for the placeholders of owner.
func fingerprintOf(t *testing.T, owner, hub string) types.String {
	t.Helper()
	fp := fingerprint(jsondoc.NewValue(ModulesContentType, owner), types.StringNull(), []byte(hub))
	if fp.IsNull() {
		t.Fatalf("no fingerprint of %s", hub)
	}
	return fp
}

// TestPlanModulesContent: the replacement decision of CONCEPT.md §6.7 —
// which attribute to blame, and when — from plan, state, the configured
// values and the state's fingerprint alone.
func TestPlanModulesContent(t *testing.T) {
	rotated := map[string]string{"acr": "new", "conn": "Endpoint=x"}
	renamed := map[string]string{"registry": "p4ss", "conn": "Endpoint=x"}
	plain := resolved(values)
	noTokens := `{"$edgeAgent":{"properties.desired":{"modules":{}}}}`
	migrating := strings.Replace(tokenized, `"{{secret.acr}}"`, `"p4ss"`, 1) // acr still a literal, conn already a placeholder
	renamedDoc := strings.Replace(tokenized, `"{{secret.acr}}"`, `"{{secret.registry}}"`, 1)
	fp := fingerprintOf(t, tokenized, plain)
	none := types.StringNull()
	cases := []struct {
		name        string
		plan, state string // "" state: create
		values      map[string]string
		valuesKnown bool
		fingerprint types.String // of state
		wantReplace string       // attribute, or ""
		wantWarning string       // substring, or ""
	}{
		{"create", tokenized, "", values, true, fp, "", ""},
		{"document changed", strings.Replace(tokenized, `"routes":{}`, `"routes":{"r":"FROM /* INTO $upstream"}`, 1), tokenized, values, true, fp, "modules_content", ""},
		{"steady state", tokenized, tokenized, values, true, fp, "", ""},
		{"rotated in config", tokenized, tokenized, rotated, true, fp, "secrets_fingerprint", ""},
		{"recreated on the hub", tokenized, tokenized, values, true, fingerprintOf(t, tokenized, resolved(map[string]string{"acr": "p4ss", "conn": "other"})), "secrets_fingerprint", ""},
		{"no fingerprint", tokenized, tokenized, values, true, none, "secrets_fingerprint", ""},
		{"unreadable fingerprint", tokenized, tokenized, values, true, types.StringValue("{"), "secrets_fingerprint", ""},
		{"unknown values, same placeholders", tokenized, tokenized, map[string]string{"conn": "Endpoint=x"}, false, fp, "secrets_fingerprint", "not known until apply"},
		{"unknown values, document changed", noTokens, tokenized, map[string]string{"conn": "Endpoint=x"}, false, fp, "modules_content", ""},
		{"imported, secrets match", tokenized, plain, values, true, none, "", ""},
		{"imported, secrets differ", tokenized, plain, rotated, true, none, "modules_content", ""},
		{"placeholder replaces a literal, other placeholder unchanged", tokenized, migrating, values, true, fingerprintOf(t, migrating, plain), "", ""},
		{"placeholder replaces a literal, value differs", tokenized, migrating, rotated, true, fingerprintOf(t, migrating, plain), "modules_content", ""},
		{"placeholder dropped for its literal value", migrating, tokenized, map[string]string{"conn": values["conn"]}, true, fp, "modules_content", ""},
		{"placeholder renamed, value unchanged", renamedDoc, tokenized, renamed, true, fp, "", ""},
		{"placeholder renamed, value changed", renamedDoc, tokenized, map[string]string{"registry": "new", "conn": "Endpoint=x"}, true, fp, "secrets_fingerprint", ""},
		{"no placeholders", noTokens, noTokens, nil, true, none, "", ""},
		{"reformatted", strings.Replace(tokenized, `"routes":{}`, `"routes": { }`, 1), tokenized, values, true, fp, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			plan := edgeModel(c.plan)
			var state *model
			if c.state != "" {
				s := edgeModel(c.state)
				s.SecretsFingerprint = c.fingerprint
				state = &s
			}
			got, diags := planModulesContent(plan, state, c.values, c.valuesKnown)
			if diags.HasError() {
				t.Fatalf("errors: %v", diags)
			}
			if got != c.wantReplace {
				t.Errorf("replace = %q, want %q", got, c.wantReplace)
			}
			var warnings []string
			for _, d := range diags {
				warnings = append(warnings, d.Summary()+": "+d.Detail())
			}
			joined := strings.Join(warnings, "\n")
			if (c.wantWarning == "") != (joined == "") || !strings.Contains(joined, c.wantWarning) {
				t.Errorf("warnings = %q, want %q", joined, c.wantWarning)
			}
		})
	}
	// A document not known until apply cannot be compared: the deployment
	// is replaced.
	plan, state := edgeModel(tokenized), edgeModel(tokenized)
	state.SecretsFingerprint = fp
	plan.ModulesContent = jsondoc.Value{StringValue: types.StringUnknown()}
	if got, diags := planModulesContent(plan, &state, values, true); got != "modules_content" || len(diags) != 0 {
		t.Errorf("unknown document: replace %q, diagnostics %v", got, diags)
	}
	// A changed ID already forces replacement: a rotated secret adds nothing.
	plan = edgeModel(tokenized)
	plan.ConfigurationID = types.StringValue("dep-2")
	if got, diags := planModulesContent(plan, &state, rotated, true); got != "" || len(diags) != 0 {
		t.Errorf("changed ID: replace %q, diagnostics %v", got, diags)
	}
}

// TestFingerprint: refresh and writes keep one salted fingerprint of the
// hub's values in state, never the values; it follows the hub under the
// same salt, and a document without placeholders has none.
func TestFingerprint(t *testing.T) {
	owner := jsondoc.NewValue(ModulesContentType, tokenized)
	fp := fingerprint(owner, types.StringNull(), []byte(resolved(values)))
	if s := fp.ValueString(); s == "" || strings.Contains(s, "p4ss") || strings.Contains(s, "Endpoint=x") {
		t.Fatalf("fingerprint: %q", s)
	}
	prior, _ := twinpatch.Decode(tokenized)
	refs := secrets.Refs(prior)
	if !secrets.ParseFingerprint(fp.ValueString()).Matches(refs, values) {
		t.Error("the configured values must match a fingerprint of the same values")
	}
	if again := fingerprint(owner, fp, []byte(resolved(values))); !again.Equal(fp) {
		t.Errorf("an unchanged hub must keep the fingerprint: %s -> %s", fp, again)
	}
	// The hub changed one value: the fingerprint follows the hub.
	changed := fingerprint(owner, fp, []byte(resolved(map[string]string{"acr": "p4ss", "conn": "other"})))
	if changed.Equal(fp) || secrets.ParseFingerprint(changed.ValueString()).Matches(refs, values) {
		t.Errorf("after hub change: %s", changed)
	}
	// Unreadable hub content, no placeholders, no owner document (import): null.
	for name, got := range map[string]types.String{
		"unreadable hub content": fingerprint(owner, fp, []byte("{")),
		"no placeholders":        fingerprint(jsondoc.NewValue(ModulesContentType, `{"$edgeAgent":{}}`), fp, []byte(`{"$edgeAgent":{}}`)),
		"import":                 fingerprint(jsondoc.NewNull(ModulesContentType), fp, []byte(resolved(values))),
	} {
		if !got.IsNull() {
			t.Errorf("%s: %s", name, got)
		}
	}
}

// TestTokenizeHub: what the hub returns is stored with the placeholders of
// the owning document, so the secret never enters state.
func TestTokenizeHub(t *testing.T) {
	owner := jsondoc.NewValue(ModulesContentType, tokenized)
	hub := json.RawMessage(resolved(values))
	got := tokenizeHub(owner, hub)
	if s := string(got); strings.Contains(s, "p4ss") || strings.Contains(s, "Endpoint=x") || !jsondoc.SemanticallyEqual(s, tokenized) {
		t.Errorf("tokenized: %s", s)
	}
	var m model
	edgeDeploymentKind.fromHub(&m, &client.Configuration{Content: client.ConfigurationContent{ModulesContent: got}}, model{ModulesContent: owner})
	if m.ModulesContent.ValueString() != tokenized {
		t.Errorf("state keeps the configured string: %s", m.ModulesContent.ValueString())
	}
	// A recreated deployment with a changed non-secret value shows the change.
	drift := json.RawMessage(strings.Replace(resolved(values), `"routes":{}`, `"routes":{"r":"x"}`, 1))
	if s := string(tokenizeHub(owner, drift)); strings.Contains(s, "p4ss") || !strings.Contains(s, `"routes":{"r":"x"}`) {
		t.Errorf("drift: %s", s)
	}
	// Without an owner (import) or without tokens, the hub's document is kept.
	if s := string(tokenizeHub(jsondoc.NewNull(ModulesContentType), hub)); !strings.Contains(s, "p4ss") {
		t.Error("import must keep the hub's document")
	}
}

// TestAdoptPlanTokens: a renamed placeholder resolves with the configured
// values the way the plan compared it, so the update stays state-only.
func TestAdoptPlanTokens(t *testing.T) {
	ctx := t.Context()
	renamedDoc := strings.Replace(tokenized, `"{{secret.acr}}"`, `"{{secret.registry}}"`, 1)
	renamed := map[string]string{"registry": "p4ss", "conn": "Endpoint=x"}
	state, plan := edgeModel(tokenized), edgeModel(renamedDoc)
	if d := diffWritten(writtenFromModel(ctx, state, renamed), writtenFromModel(ctx, plan, renamed)); len(d) == 0 {
		t.Fatal("state's old token resolves nothing under the new name; the documents must differ before adoption")
	}
	state.ModulesContent = adoptPlanTokens(state.ModulesContent, plan.ModulesContent)
	if d := diffWritten(writtenFromModel(ctx, state, renamed), writtenFromModel(ctx, plan, renamed)); len(d) != 0 {
		t.Errorf("renamed placeholder: %v", d)
	}
	// Literals, null and undecodable documents pass through.
	literal := jsondoc.NewValue(ModulesContentType, resolved(values))
	if got := adoptPlanTokens(literal, plan.ModulesContent); !got.Equal(literal) {
		t.Errorf("literals must stay: %s", got)
	}
	for name, prior := range map[string]jsondoc.Value{"null": jsondoc.NewNull(ModulesContentType), "undecodable": jsondoc.NewValue(ModulesContentType, "{")} {
		if got := adoptPlanTokens(prior, plan.ModulesContent); !got.Equal(prior) {
			t.Errorf("%s: %s", name, got)
		}
	}
}

// TestSpecResolvesSecrets: the wire carries the values, written fields
// compare after resolution, and a placeholder without a value is never
// written.
func TestSpecResolvesSecrets(t *testing.T) {
	spec, diags := edgeDeploymentKind.spec(t.Context(), edgeModel(tokenized), values)
	if diags.HasError() || !jsondoc.SemanticallyEqual(string(spec.Content.ModulesContent), resolved(values)) {
		t.Errorf("spec: %v %s", diags, spec.Content.ModulesContent)
	}
	w := writtenFromModel(t.Context(), edgeModel(tokenized), values)
	if d := diffWritten(w, writtenFromHub(&client.Configuration{Content: client.ConfigurationContent{ModulesContent: json.RawMessage(resolved(values))}})); len(d) != 0 {
		t.Errorf("written: %v", d)
	}
	_, diags = edgeDeploymentKind.spec(t.Context(), edgeModel(tokenized), map[string]string{"acr": "p4ss"})
	if !diags.HasError() || !strings.Contains(diags[0].Detail(), "`{{secret.conn}}`") {
		t.Errorf("unresolved placeholder must not be written: %v", diags)
	}
}
