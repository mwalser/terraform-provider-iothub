package provider_test

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	tfjson "github.com/hashicorp/terraform-json"
	"github.com/hashicorp/terraform-plugin-testing/compare"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	iotacc "github.com/mwalser/terraform-provider-iothub/internal/acctest"
	"github.com/mwalser/terraform-provider-iothub/internal/client"
	"github.com/mwalser/terraform-provider-iothub/internal/twinpatch"
)

// changeOf returns the planned change of the resource at address.
func changeOf(req plancheck.CheckPlanRequest, address string) (*tfjson.Change, error) {
	for _, rc := range req.Plan.ResourceChanges {
		if rc.Address == address && rc.Change != nil {
			return rc.Change, nil
		}
	}
	return nil, fmt.Errorf("%s not in the plan", address)
}

// expectNotSensitive is a plan check that fails when the attribute is marked
// sensitive in the plan — the whole point of secret placeholders is that the
// document keeps its diff.
type expectNotSensitive struct{ address, attribute string }

func (e expectNotSensitive) CheckPlan(_ context.Context, req plancheck.CheckPlanRequest, resp *plancheck.CheckPlanResponse) {
	change, err := changeOf(req, e.address)
	if err != nil {
		resp.Error = err
		return
	}
	if m, ok := change.AfterSensitive.(map[string]any); ok {
		if v, _ := m[e.attribute].(bool); v {
			resp.Error = fmt.Errorf("%s.%s is sensitive in the plan", e.address, e.attribute)
		}
	}
}

// expectAbsentFromPlan is a plan check that fails when the attribute's
// prior or planned value contains text — an out-of-band secret that must
// not have entered state.
type expectAbsentFromPlan struct{ address, attribute, text string }

func (e expectAbsentFromPlan) CheckPlan(_ context.Context, req plancheck.CheckPlanRequest, resp *plancheck.CheckPlanResponse) {
	change, err := changeOf(req, e.address)
	if err != nil {
		resp.Error = err
		return
	}
	for _, side := range []any{change.Before, change.After} {
		if m, ok := side.(map[string]any); ok {
			if s, _ := m[e.attribute].(string); strings.Contains(s, e.text) {
				resp.Error = fmt.Errorf("%s.%s contains %q in the plan", e.address, e.attribute, e.text)
			}
		}
	}
}

// expectReplaceOn is a plan check that fails unless the replacement is
// forced by the attribute — the line the plan marks `# forces replacement`.
type expectReplaceOn struct{ address, attribute string }

func (e expectReplaceOn) CheckPlan(_ context.Context, req plancheck.CheckPlanRequest, resp *plancheck.CheckPlanResponse) {
	change, err := changeOf(req, e.address)
	if err != nil {
		resp.Error = err
		return
	}
	for _, p := range change.ReplacePaths {
		if steps, ok := p.([]any); ok && len(steps) == 1 && steps[0] == e.attribute {
			return
		}
	}
	resp.Error = fmt.Errorf("%s: replacement not forced by %s (replace paths: %v)", e.address, e.attribute, change.ReplacePaths)
}

// hubSecrets reads what the hub holds at the two placeholder paths.
func hubSecrets(t *testing.T, id string) (password, conn string, err error) {
	t.Helper()
	cfg, err := iotacc.Client(t).GetConfiguration(context.Background(), id)
	if err != nil {
		return "", "", err
	}
	doc, err := twinpatch.Decode(string(cfg.Content.ModulesContent))
	if err != nil {
		return "", "", err
	}
	p, _ := twinpatch.Get(doc, twinpatch.Path{"$edgeAgent", "properties.desired", "runtime", "settings", "registryCredentials", "acr", "password"})
	c, _ := twinpatch.Get(doc, twinpatch.Path{"$edgeAgent", "properties.desired", "modules", "collector", "env", "CONN", "value"})
	ps, _ := p.(string)
	cs, _ := c.(string)
	return ps, cs, nil
}

func checkHubSecrets(t *testing.T, id, password, conn string) func(*terraform.State) error {
	return func(_ *terraform.State) error {
		p, c, err := hubSecrets(t, id)
		if err != nil {
			return err
		}
		if p != password || c != conn {
			return fmt.Errorf("hub holds password %q and CONN %q, want %q and %q", p, c, password, conn)
		}
		return nil
	}
}

// TestAccEdgeDeployment_secrets: placeholders in modules_content backed by
// the write-only secrets_wo map (CONCEPT.md §6.7). The document stays
// unmarked in the plan, state holds the placeholders, the hub holds the
// values; a rotated secret and a deployment recreated outside Terraform are
// both replacements; an imported deployment adopts the placeholders with an
// in-place, state-only update.
func TestAccEdgeDeployment_secrets(t *testing.T) {
	id := acctest.RandomWithPrefix("tf-acc")
	res := "iothub_edge_deployment.test"
	cfgAcr := func(acr string, priority int, password, conn string) string {
		return iotacc.ProviderConfig() + fmt.Sprintf(`
variable "acr_password" {
  type      = string
  sensitive = true
  default   = %q
}

resource "iothub_edge_deployment" "test" {
  deployment_id    = %q
  target_condition = "tags.site = 'munich'"
  priority         = %d
  modules_content = provider::iothub::edge_manifest({
    registry_credentials = {
      acr = { address = "contoso.azurecr.io", username = "ci", password = "{{secret.acr}}" }
    }
    edge_agent = { image = "mcr.microsoft.com/azureiotedge-agent:1.5" }
    edge_hub   = { image = "mcr.microsoft.com/azureiotedge-hub:1.5" }
    modules = {
      collector = {
        image = "contoso.azurecr.io/collector:2.1.0"
        env   = { CONN = "{{secret.conn}}", Verbose = true }
      }
    }
    routes = { upstream = "FROM /messages/* INTO $upstream" }
  })
  secrets_wo = {
    acr  = %s
    conn = %q
  }
}
`, password, id, priority, acr, conn)
	}
	cfg := func(priority int, password, conn string) string {
		return cfgAcr("var.acr_password", priority, password, conn)
	}
	// forget drops the deployment from state without destroying it, so the
	// next step can import it.
	forget := iotacc.ProviderConfig() + `
removed {
  from = iothub_edge_deployment.test
  lifecycle {
    destroy = false
  }
}
`
	tokenAt := func(p string) statecheck.StateCheck {
		return statecheck.ExpectKnownValue(res, tfjsonpath.New("modules_content"), knownvalue.StringRegexp(regexp.MustCompile(regexp.QuoteMeta(p))))
	}
	stateChecks := []statecheck.StateCheck{
		tokenAt(`"password":"{{secret.acr}}"`),
		tokenAt(`"CONN":{"value":"{{secret.conn}}"}`),
		statecheck.ExpectKnownValue(res, tfjsonpath.New("secrets_wo"), knownvalue.Null()),
		statecheck.ExpectKnownValue(res, tfjsonpath.New("secrets_fingerprint"), knownvalue.NotNull()),
	}
	with := func(checks ...statecheck.StateCheck) []statecheck.StateCheck {
		return append(slices.Clone(stateChecks), checks...)
	}
	// A secret that is not the hub's replaces the deployment, and the plan
	// says so on the fingerprint.
	replacedOnFingerprint := []plancheck.PlanCheck{
		plancheck.ExpectResourceAction(res, plancheck.ResourceActionReplace),
		expectReplaceOn{res, "secrets_fingerprint"},
		plancheck.ExpectUnknownValue(res, tfjsonpath.New("secrets_fingerprint")),
	}
	// Across steps: refreshes and in-place updates of an unchanged hub keep
	// the fingerprint, a refresh keeps the ETag; a recreation with the same
	// values keeps the fingerprint; the import adoption keeps the ETag —
	// state only, hub untouched.
	fingerprintPath, etagPath := tfjsonpath.New("secrets_fingerprint"), tfjsonpath.New("etag")
	fingerprintAcrossUpdates, etagAcrossRefresh := statecheck.CompareValue(compare.ValuesSame()), statecheck.CompareValue(compare.ValuesSame())
	fingerprintAcrossRecreate, etagAcrossImport := statecheck.CompareValue(compare.ValuesSame()), statecheck.CompareValue(compare.ValuesSame())
	// unknownAcr feeds the ACR password through a resource created in the
	// same apply, so its value is not known until apply.
	unknownAcr := func(conn string) string {
		return cfgAcr("terraform_data.rotation.output", 10, "p2", conn) + `
resource "terraform_data" "rotation" {
  input = "p2"
}
`
	}
	recreate := func(password string) func() {
		return func() {
			c := iotacc.Client(t)
			ctx := context.Background()
			cur, err := c.GetConfiguration(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			doc, err := twinpatch.Decode(string(cur.Content.ModulesContent))
			if err != nil {
				t.Fatal(err)
			}
			twinpatch.Set(doc, twinpatch.Path{"$edgeAgent", "properties.desired", "runtime", "settings", "registryCredentials", "acr", "password"}, password)
			if err := c.DeleteConfiguration(ctx, id, "*"); err != nil {
				t.Fatal(err)
			}
			_, err = c.CreateConfiguration(ctx, client.ConfigurationSpec{ID: id, TargetCondition: cur.TargetCondition, Priority: cur.Priority,
				Content: client.ConfigurationContent{ModulesContent: json.RawMessage(twinpatch.Encode(doc))}})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { iotacc.PreCheck(t) },
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactories,
		CheckDestroy:             checkConfigurationDestroyed(id),
		Steps: []resource.TestStep{
			{
				Config: cfg(10, "p1", "Endpoint=sb://x/;SharedAccessKey=k1"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					expectNotSensitive{res, "modules_content"},
					plancheck.ExpectKnownValue(res, tfjsonpath.New("modules_content"), knownvalue.StringRegexp(regexp.MustCompile(`\{\{secret\.acr\}\}`))),
				}},
				ConfigStateChecks: with(fingerprintAcrossUpdates.AddStateValue(res, fingerprintPath), etagAcrossRefresh.AddStateValue(res, etagPath)),
				Check:             checkHubSecrets(t, id, "p1", "Endpoint=sb://x/;SharedAccessKey=k1"),
			},
			{ // a refresh of an unchanged hub changes nothing, the fingerprint included
				Config:            cfg(10, "p1", "Endpoint=sb://x/;SharedAccessKey=k1"),
				ConfigPlanChecks:  resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				ConfigStateChecks: with(fingerprintAcrossUpdates.AddStateValue(res, fingerprintPath), etagAcrossRefresh.AddStateValue(res, etagPath)),
			},
			{ // an in-place update carries the resolved content and keeps the placeholders in state
				Config:            cfg(11, "p1", "Endpoint=sb://x/;SharedAccessKey=k1"),
				ConfigPlanChecks:  resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)}},
				ConfigStateChecks: with(statecheck.ExpectKnownValue(res, tfjsonpath.New("priority"), knownvalue.Int64Exact(11)), fingerprintAcrossUpdates.AddStateValue(res, fingerprintPath)),
				Check:             checkHubSecrets(t, id, "p1", "Endpoint=sb://x/;SharedAccessKey=k1"),
			},
			{ // rotated in the configuration: replacement
				Config:            cfg(10, "p2", "Endpoint=sb://x/;SharedAccessKey=k1"),
				ConfigPlanChecks:  resource.ConfigPlanChecks{PreApply: replacedOnFingerprint},
				ConfigStateChecks: stateChecks,
				Check:             checkHubSecrets(t, id, "p2", "Endpoint=sb://x/;SharedAccessKey=k1"),
			},
			{ // recreated outside Terraform with another password: replacement, and the password never enters state
				PreConfig:        recreate("p3"),
				Config:           cfg(10, "p2", "Endpoint=sb://x/;SharedAccessKey=k1"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: append(replacedOnFingerprint, expectAbsentFromPlan{res, "modules_content", "p3"})},
				Check:            checkHubSecrets(t, id, "p2", "Endpoint=sb://x/;SharedAccessKey=k1"),
			},
			{ // a second secret changes: replacement
				Config:            cfg(10, "p2", "Endpoint=sb://x/;SharedAccessKey=k2"),
				ConfigPlanChecks:  resource.ConfigPlanChecks{PreApply: replacedOnFingerprint},
				ConfigStateChecks: with(fingerprintAcrossRecreate.AddStateValue(res, fingerprintPath)),
				Check:             checkHubSecrets(t, id, "p2", "Endpoint=sb://x/;SharedAccessKey=k2"),
			},
			{ // recreated outside Terraform with the same values: nothing to do
				PreConfig:         recreate("p2"),
				Config:            cfg(10, "p2", "Endpoint=sb://x/;SharedAccessKey=k2"),
				ConfigPlanChecks:  resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				ConfigStateChecks: with(fingerprintAcrossRecreate.AddStateValue(res, fingerprintPath)),
			},
			{ // a value not known until apply cannot be compared: replacement on the fingerprint
				Config:            unknownAcr("Endpoint=sb://x/;SharedAccessKey=k2"),
				ConfigPlanChecks:  resource.ConfigPlanChecks{PreApply: replacedOnFingerprint},
				ConfigStateChecks: with(etagAcrossImport.AddStateValue(res, etagPath)),
				Check:             checkHubSecrets(t, id, "p2", "Endpoint=sb://x/;SharedAccessKey=k2"),
			},
			{Config: forget},
			{ // import brings the hub's values into state …
				Config:             cfg(10, "p2", "Endpoint=sb://x/;SharedAccessKey=k2"),
				ResourceName:       res,
				ImportState:        true,
				ImportStateId:      id,
				ImportStatePersist: true,
				ImportStateCheck: func(states []*terraform.InstanceState) error {
					if len(states) != 1 || !strings.Contains(states[0].Attributes["modules_content"], `"password":"p2"`) {
						return fmt.Errorf("imported state does not hold the hub's document: %v", states)
					}
					if fp := states[0].Attributes["secrets_fingerprint"]; fp != "" {
						return fmt.Errorf("imported state has a fingerprint without placeholders: %q", fp)
					}
					return nil
				},
			},
			{ // … and the placeholders take over with an in-place, state-only update
				Config:            cfg(10, "p2", "Endpoint=sb://x/;SharedAccessKey=k2"),
				ConfigPlanChecks:  resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(res, plancheck.ResourceActionUpdate)}},
				ConfigStateChecks: with(etagAcrossImport.AddStateValue(res, etagPath)),
				Check:             checkHubSecrets(t, id, "p2", "Endpoint=sb://x/;SharedAccessKey=k2"),
			},
			{Config: forget},
			{ // an imported deployment whose secret does not match the configuration is replaced
				Config:             cfg(10, "p4", "Endpoint=sb://x/;SharedAccessKey=k2"),
				ResourceName:       res,
				ImportState:        true,
				ImportStateId:      id,
				ImportStatePersist: true,
			},
			{
				Config: cfg(10, "p4", "Endpoint=sb://x/;SharedAccessKey=k2"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(res, plancheck.ResourceActionReplace),
					expectReplaceOn{res, "modules_content"},
				}},
				Check: checkHubSecrets(t, id, "p4", "Endpoint=sb://x/;SharedAccessKey=k2"),
			},
		},
	})
}

// TestAccEdgeDeployment_secretsValidation: placeholders and keys must
// correspond, before anything is written.
func TestAccEdgeDeployment_secretsValidation(t *testing.T) {
	id := acctest.RandomWithPrefix("tf-acc")
	cfg := func(secrets string) string {
		return iotacc.ProviderConfig() + fmt.Sprintf(`
resource "iothub_edge_deployment" "test" {
  deployment_id    = %q
  target_condition = "*"
  modules_content = jsonencode({
    "$edgeAgent" = { "properties.desired.modules.m" = { type = "docker", settings = { image = "x" }, env = { K = { value = "{{secret.k}}" } } } }
  })
  %s
}
`, id, secrets)
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { iotacc.PreCheck(t) },
		ProtoV6ProviderFactories: iotacc.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Terraform wraps diagnostics, so the patterns tolerate line breaks.
			{Config: cfg(""), PlanOnly: true, ExpectError: regexp.MustCompile("`secrets_wo`\\s+is\\s+not\\s+set")},
			{Config: cfg(`secrets_wo = { other = "v" }`), PlanOnly: true, ExpectError: regexp.MustCompile("has\\s+no\\s+key\\s+`k`")},
			{Config: cfg(`secrets_wo = { k = "v", other = "v" }`), PlanOnly: true, ExpectError: regexp.MustCompile(`Unused\s+secret`)},
			{Config: cfg(`secrets_wo = { "bad key" = "v" }`), PlanOnly: true, ExpectError: regexp.MustCompile(`must\s+be\s+letters,\s+digits`)},
			{Config: cfg(`secrets_wo = {}`), PlanOnly: true, ExpectError: regexp.MustCompile(`at\s+least\s+1\s+elements`)},
		},
	})
}
