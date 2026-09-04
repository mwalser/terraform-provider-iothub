package configuration

import (
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/mwalser/terraform-provider-iothub/internal/provider/secrets"
	"github.com/mwalser/terraform-provider-iothub/internal/twinpatch"
)

// TestRead_TokenizesHub: a refresh stores what the hub returns with the
// prior state's placeholders and a fingerprint of the values, so a secret
// never enters state (CONCEPT.md §6.7).
func TestRead_TokenizesHub(t *testing.T) {
	ctx := t.Context()
	_, objType := edgeSchema(t)
	resp := readDeployment(t, edgeRaw(t, objType, deployment(t, tokenized, tftypes.NewValue(tftypes.String, nil))), resolved(values))
	if raw := resp.State.Raw.String(); strings.Contains(raw, "p4ss") || strings.Contains(raw, "Endpoint=x") {
		t.Fatalf("a value reached state: %s", raw)
	}
	m, diags := edgeDeploymentKind.get(ctx, resp.State)
	if diags.HasError() {
		t.Fatal(diags)
	}
	if m.ModulesContent.ValueString() != tokenized {
		t.Errorf("state must keep the placeholders: %s", m.ModulesContent)
	}
	prior, _ := twinpatch.Decode(tokenized)
	if m.SecretsFingerprint.IsNull() || !secrets.ParseFingerprint(m.SecretsFingerprint.ValueString()).Matches(secrets.Refs(prior), values) {
		t.Errorf("fingerprint of the hub's values: %s", m.SecretsFingerprint)
	}
	if m.ETag.ValueString() != "MQ==" || m.Priority.ValueInt64() != 10 {
		t.Errorf("hub fields: etag %s, priority %d", m.ETag, m.Priority.ValueInt64())
	}
}
