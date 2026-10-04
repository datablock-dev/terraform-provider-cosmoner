package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
)

func TestVariableResource(t *testing.T) {
	clearEnv(t)
	api := newFakeAPI(t)
	const name = "cosmoner_variable.log_level"

	config := func(value, description string) string {
		desc := ""
		if description != "" {
			desc = fmt.Sprintf("description = %q", description)
		}
		return providerConfig(api) + fmt.Sprintf(`
resource "cosmoner_variable" "log_level" {
  name        = "LOG_LEVEL"
  environment = "staging"
  value       = %q
  %s
}
`, value, desc)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkAPICount(api, 0),
		Steps: []resource.TestStep{
			{
				Config: config("info", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(name, "id"),
					resource.TestCheckResourceAttr(name, "project_id", testProjectID),
					resource.TestCheckResourceAttr(name, "environment", "staging"),
					resource.TestCheckResourceAttr(name, "value", "info"),
					resource.TestCheckNoResourceAttr(name, "description"),
				),
			},
			{
				Config: config("debug", "Verbosity"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(name, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(name, "value", "debug"),
					resource.TestCheckResourceAttr(name, "description", "Verbosity"),
				),
			},
			{
				Config: config("debug", ""),
				Check:  resource.TestCheckNoResourceAttr(name, "description"),
			},
			{
				// Deleted outside Terraform: the next plan creates it again.
				PreConfig: api.deleteAllVariables,
				Config:    config("debug", ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(name, plancheck.ResourceActionCreate)},
				},
			},
			{
				ResourceName:      name,
				ImportState:       true,
				ImportStateIdFunc: importID(name),
				ImportStateVerify: true,
			},
		},
	})
}

func TestVariableResourceFollowsTheProviderProject(t *testing.T) {
	clearEnv(t)
	api := newFakeAPI(t)
	const name = "cosmoner_variable.region"

	config := func(projectID string) string {
		return fmt.Sprintf(`
provider "cosmoner" {
  api_key     = %q
  project_id  = %q
  api_url     = %q
  max_retries = 0
}

resource "cosmoner_variable" "region" {
  name  = "REGION"
  value = "eu-north-1"
}
`, testAPIKey, projectID, api.server.URL)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkAPICount(api, 0),
		Steps: []resource.TestStep{
			{
				Config: config(testProjectID),
				// Known at plan time, not "(known after apply)".
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectKnownValue(name, tfjsonPath("project_id"), knownString(testProjectID))},
				},
			},
			{
				Config: config(otherProjectID),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(name, plancheck.ResourceActionReplace)},
				},
				Check: resource.TestCheckResourceAttr(name, "project_id", otherProjectID),
			},
		},
	})
}
