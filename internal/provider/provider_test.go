package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// These tests run real Terraform against fakeAPI, through the whole plan,
// apply, refresh and import cycle, so they need a terraform binary: on PATH,
// at TF_ACC_TERRAFORM_PATH, or downloaded by the test harness. They need no
// credentials and touch no real project.

func protoV6ProviderFactories() map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"cosmoner": providerserver.NewProtocol6WithError(New("test")()),
	}
}

// providerConfig points the provider at the fake, with the test project as
// its default. Retries are off so a failing request fails the step at once.
func providerConfig(api *fakeAPI) string {
	return fmt.Sprintf(`
provider "cosmoner" {
  api_key     = %q
  project_id  = %q
  api_url     = %q
  max_retries = 0
}
`, testAPIKey, testProjectID, api.server.URL)
}

// clearEnv keeps a developer's own credentials out of the tests.
func clearEnv(t *testing.T) {
	t.Setenv(envAPIKey, "")
	t.Setenv(envProjectID, "")
	t.Setenv(envAPIURL, "")
}

// importID builds the `<project_id>/<id>` form for a resource in state.
func importID(name string) resource.ImportStateIdFunc {
	return func(s *terraform.State) (string, error) {
		rs, ok := s.RootModule().Resources[name]
		if !ok {
			return "", fmt.Errorf("%s not in state", name)
		}
		return rs.Primary.Attributes["project_id"] + "/" + rs.Primary.ID, nil
	}
}

// checkAPICount asserts how many resources the fake holds, which is how a
// test proves a destroy really deleted something.
func checkAPICount(api *fakeAPI, want int) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if got := api.count(); got != want {
			return fmt.Errorf("fake API holds %d resources, want %d", got, want)
		}
		return nil
	}
}

func TestProviderRequiresAnAPIKey(t *testing.T) {
	clearEnv(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config: `
provider "cosmoner" {
  project_id = "proj_test"
}

data "cosmoner_project" "this" {}
`,
			ExpectError: regexp.MustCompile(`Missing API key`),
		}},
	})
}

func TestProviderReadsTheEnvironment(t *testing.T) {
	api := newFakeAPI(t)
	t.Setenv(envAPIKey, testAPIKey)
	t.Setenv(envProjectID, testProjectID)
	t.Setenv(envAPIURL, api.server.URL)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config: `data "cosmoner_project" "this" {}`,
			Check:  resource.TestCheckResourceAttr("data.cosmoner_project.this", "id", testProjectID),
		}},
	})
}

func TestProviderRejectsABadKey(t *testing.T) {
	clearEnv(t)
	api := newFakeAPI(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config: fmt.Sprintf(`
provider "cosmoner" {
  api_key = "wrong"
  api_url = %q
}

data "cosmoner_project" "this" {
  id = "proj_test"
}
`, api.server.URL),
			ExpectError: regexp.MustCompile(`Invalid API key`),
		}},
	})
}

func TestResourcesNeedAProject(t *testing.T) {
	clearEnv(t)
	api := newFakeAPI(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config: fmt.Sprintf(`
provider "cosmoner" {
  api_key = %q
  api_url = %q
}

resource "cosmoner_variable" "this" {
  name  = "LOG_LEVEL"
  value = "debug"
}
`, testAPIKey, api.server.URL),
			ExpectError: regexp.MustCompile(`Missing project`),
		}},
	})
}

func TestProjectDataSource(t *testing.T) {
	clearEnv(t)
	api := newFakeAPI(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config: providerConfig(api) + `
data "cosmoner_project" "default" {}

data "cosmoner_project" "other" {
  id = "proj_other"
}
`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.cosmoner_project.default", "id", testProjectID),
				resource.TestCheckResourceAttr("data.cosmoner_project.default", "name", "Test project"),
				resource.TestCheckResourceAttr("data.cosmoner_project.default", "slug", "test-project"),
				resource.TestCheckResourceAttr("data.cosmoner_project.default", "billing_email", "billing@example.com"),
				resource.TestCheckResourceAttr("data.cosmoner_project.default", "blocked", "false"),
				resource.TestCheckResourceAttr("data.cosmoner_project.other", "id", otherProjectID),
				resource.TestCheckNoResourceAttr("data.cosmoner_project.other", "billing_email"),
				resource.TestCheckResourceAttr("data.cosmoner_project.other", "blocked", "true"),
			),
		}},
	})
}
