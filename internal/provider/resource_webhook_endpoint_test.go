package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
)

func TestWebhookEndpointResource(t *testing.T) {
	clearEnv(t)
	api := newFakeAPI(t)
	const name = "cosmoner_webhook_endpoint.deploys"

	config := func(events, extra string) string {
		return providerConfig(api) + fmt.Sprintf(`
resource "cosmoner_webhook_endpoint" "deploys" {
  name   = "Deploys"
  url    = "https://hooks.example.com/cosmoner"
  events = %s
  %s
}
`, events, extra)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkAPICount(api, 0),
		Steps: []resource.TestStep{
			{
				// Created disabled: the create route takes no flag, so this is
				// a create followed by an update.
				Config: config(`["app.deployed"]`, `enabled = false
  description = "Deploy notifications"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(name, "id"),
					resource.TestCheckResourceAttr(name, "enabled", "false"),
					resource.TestCheckResourceAttr(name, "disabled_reason", "MANUAL"),
					resource.TestCheckResourceAttr(name, "description", "Deploy notifications"),
					resource.TestCheckResourceAttrSet(name, "signing_secret"),
					resource.TestCheckResourceAttr(name, "secret_hint", "abcd"),
					resource.TestCheckResourceAttr(name, "events.#", "1"),
				),
			},
			{
				// The description is removed, which the API needs as an explicit null.
				Config: config(`["app.failed", "app.deployed"]`, ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(name, "enabled", "true"),
					resource.TestCheckNoResourceAttr(name, "disabled_reason"),
					resource.TestCheckNoResourceAttr(name, "description"),
					resource.TestCheckTypeSetElemAttr(name, "events.*", "app.failed"),
					resource.TestCheckTypeSetElemAttr(name, "events.*", "app.deployed"),
					resource.TestCheckResourceAttrSet(name, "signing_secret"),
				),
			},
			{
				// Reordering a set is not a change.
				Config: config(`["app.deployed", "app.failed"]`, ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// Paused by the API after repeated failures: the next apply
				// turns it back on.
				PreConfig: api.pauseWebhooks,
				Config:    config(`["app.deployed", "app.failed"]`, ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(name, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(name, "enabled", "true"),
					resource.TestCheckNoResourceAttr(name, "disabled_reason"),
				),
			},
			{
				ResourceName:      name,
				ImportState:       true,
				ImportStateIdFunc: importID(name),
				ImportStateVerify: true,
				// Only the create response carries it.
				ImportStateVerifyIgnore: []string{"signing_secret"},
			},
		},
	})
}

func TestWebhookEndpointResourceNeedsHTTPS(t *testing.T) {
	clearEnv(t)
	api := newFakeAPI(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config: providerConfig(api) + `
resource "cosmoner_webhook_endpoint" "plain" {
  name   = "Plain"
  url    = "http://hooks.example.com"
  events = ["app.deployed"]
}
`,
			PlanOnly:    true,
			ExpectError: regexp.MustCompile(`must be an https:// URL`),
		}},
	})
}
