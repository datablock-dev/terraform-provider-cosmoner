package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
)

func TestAppResource(t *testing.T) {
	clearEnv(t)
	api := newFakeAPI(t)
	const name = "cosmoner_app.web"

	config := func(image, extra string) string {
		return providerConfig(api) + fmt.Sprintf(`
resource "cosmoner_app" "web" {
  region             = "se-sto"
  container_registry = "dockerhub"
  container_image    = %q
  container_port     = 8080
  %s
}
`, image, extra)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkAPICount(api, 0),
		Steps: []resource.TestStep{
			{
				// k8s-s is a retired alias the API stores as shared-s, and the
				// region is not where the platform places the app: both keep
				// their configured spelling, or every apply would be inconsistent.
				Config: config("nginx:1.27", `name = "Web"
  size      = "k8s-s"
  instances = 2`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(name, "id"),
					resource.TestCheckResourceAttr(name, "status", "RUNNING"),
					resource.TestCheckResourceAttr(name, "url", "https://Web-x7k2.cosmoner.app"),
					resource.TestCheckResourceAttr(name, "size", "k8s-s"),
					resource.TestCheckResourceAttr(name, "region", "se-sto"),
					resource.TestCheckResourceAttr(name, "image_deploy_policy", "TAG"),
					resource.TestCheckResourceAttr(name, "container_port", "8080"),
					resource.TestCheckResourceAttr(name, "instances", "2"),
					resource.TestCheckResourceAttr(name, "auto_deploy", "false"),
				),
			},
			{
				// Settings and size change in place.
				Config: config("nginx:1.27", `name = "Website"
  size        = "shared-m"
  run_command = "nginx -g 'daemon off;'"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(name, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(name, "name", "Website"),
					resource.TestCheckResourceAttr(name, "size", "shared-m"),
					resource.TestCheckResourceAttr(name, "run_command", "nginx -g 'daemon off;'"),
					// Left out of configuration, the count the API cannot unset stays.
					resource.TestCheckResourceAttr(name, "instances", "2"),
				),
			},
			{
				Config: config("nginx:1.27", `name = "Website"
  size        = "shared-m"
  run_command = "nginx -g 'daemon off;'"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				ResourceName:      name,
				ImportState:       true,
				ImportStateIdFunc: importID(name),
				ImportStateVerify: true,
				// An import reads back where the app runs, not what was asked for.
				ImportStateVerifyIgnore: []string{"region"},
			},
			{
				// The API cannot change an app's source.
				Config: config("nginx:1.28", `name = "Website"
  size = "shared-m"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(name, plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check: resource.TestCheckResourceAttr(name, "container_image", "nginx:1.28"),
			},
		},
	})
}

func TestAppResourceFailedDeployIsTainted(t *testing.T) {
	clearEnv(t)
	api := newFakeAPI(t)
	const name = "cosmoner_app.broken"
	config := providerConfig(api) + `
resource "cosmoner_app" "broken" {
  name            = "broken"
  size            = "shared-s"
  region          = "se-sto"
  container_image = "example/unpullable:latest"
}
`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkAPICount(api, 0),
		Steps: []resource.TestStep{
			{
				Config:      config,
				ExpectError: regexp.MustCompile(`(?s)did\s+not\s+become\s+ready:\s+status\s+ERROR:\s+The\s+image\s+could\s+not\s+be\s+pulled`),
			},
			{
				// Kept in state as tainted, so the failed app is replaced, not
				// left billing outside Terraform.
				// The next apply replaces the tainted one (which here fails the
				// same way again); CheckDestroy then proves neither is left.
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(name, plancheck.ResourceActionReplace)},
				},
				ExpectError: regexp.MustCompile(`(?s)did\s+not\s+become\s+ready:\s+status\s+ERROR:\s+The\s+image\s+could\s+not\s+be\s+pulled`),
			},
		},
	})
}

func TestAppResourceAdoptsAnAppWhoseCreateAnswerWasLost(t *testing.T) {
	clearEnv(t)
	api := newFakeAPI(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkAPICount(api, 0),
		Steps: []resource.TestStep{{
			Config: providerConfig(api) + `
resource "cosmoner_app" "flaky" {
  name     = "flaky"
  size     = "shared-s"
  region   = "se-sto"
  git_repo = "acme/site"
}
`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet("cosmoner_app.flaky", "id"),
				resource.TestCheckResourceAttr("cosmoner_app.flaky", "status", "RUNNING"),
				checkAPICount(api, 1),
			),
		}},
	})
}

func TestAppResourceNeedsOneSource(t *testing.T) {
	clearEnv(t)
	api := newFakeAPI(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config: providerConfig(api) + `
resource "cosmoner_app" "both" {
  name            = "both"
  size            = "shared-s"
  region          = "se-sto"
  git_repo        = "acme/site"
  container_image = "nginx:1.27"
}
`,
			PlanOnly:    true,
			ExpectError: regexp.MustCompile(`(?s)Invalid Attribute Combination`),
		}, {
			Config: providerConfig(api) + `
resource "cosmoner_app" "neither" {
  name   = "neither"
  size   = "shared-s"
  region = "se-sto"
}
`,
			PlanOnly:    true,
			ExpectError: regexp.MustCompile(`(?s)Invalid Attribute Combination`),
		}},
	})
}

func TestAppResourceRefusedCreateLeavesNothing(t *testing.T) {
	clearEnv(t)
	api := newFakeAPI(t)
	config := func(resourceName string) string {
		return fmt.Sprintf(`
resource "cosmoner_app" %q {
  name            = "web"
  size            = "shared-s"
  region          = "se-sto"
  container_image = "nginx:1.27"
}
`, resourceName)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkAPICount(api, 0),
		Steps: []resource.TestStep{
			{Config: providerConfig(api) + config("first")},
			{
				// App names are unique in a project.
				Config:      providerConfig(api) + config("first") + config("second"),
				ExpectError: regexp.MustCompile(`already exists`),
				Check:       checkAPICount(api, 1),
			},
		},
	})
}
