package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
)

func TestBucketResource(t *testing.T) {
	clearEnv(t)
	api := newFakeAPI(t)
	const name = "cosmoner_bucket.assets"

	config := func(body string) string {
		return providerConfig(api) + fmt.Sprintf(`
resource "cosmoner_bucket" "assets" {
  name   = "assets"
  region = "eu-north-1"
  %s
}
`, body)
	}
	updated := `tier          = "GROWTH"
  public_access = true
  public_paths  = ["images/", "docs/"]
  cdn_enabled   = true
  cors_rules    = []`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkAPICount(api, 0),
		Steps: []resource.TestStep{
			{
				// The API fills the header lists a rule leaves out; the
				// defaults match, so the plan and the result agree.
				Config: config(`public_access = true
  public_paths  = ["images/"]
  cors_rules = [{
    allowed_origins = ["https://example.com"]
    allowed_methods = ["GET", "HEAD"]
    max_age_seconds = 600
  }]`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(name, "id"),
					resource.TestCheckResourceAttr(name, "status", "ACTIVE"),
					resource.TestCheckResourceAttr(name, "tier", "STARTER"),
					resource.TestCheckResourceAttrSet(name, "bucket_name"),
					resource.TestCheckResourceAttr(name, "endpoint", "https://s3.eu-north-1.example.com"),
					resource.TestCheckResourceAttr(name, "cors_rules.0.allowed_headers.#", "0"),
					resource.TestCheckResourceAttr(name, "cors_rules.0.max_age_seconds", "600"),
					resource.TestCheckNoResourceAttr(name, "cdn_domain"),
				),
			},
			{
				// Each changed setting has its own route.
				Config: config(updated),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(name, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(name, "tier", "GROWTH"),
					resource.TestCheckResourceAttr(name, "public_paths.#", "2"),
					resource.TestCheckResourceAttr(name, "cors_rules.#", "0"),
					resource.TestCheckResourceAttrSet(name, "cdn_domain"),
				),
			},
			{
				Config: config(updated),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				ResourceName:      name,
				ImportState:       true,
				ImportStateIdFunc: importID(name),
				ImportStateVerify: true,
			},
			{
				// The API cannot change versioning.
				Config: config(updated + `
  versioning = true`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(name, plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check: resource.TestCheckResourceAttr(name, "versioning", "true"),
			},
		},
	})
}

func TestBucketResourceValidatesPublicPaths(t *testing.T) {
	clearEnv(t)
	api := newFakeAPI(t)
	for path, message := range map[string]string{
		"images":     `ending in '/'`,
		"a/../":      `must not contain '\.' or '\.\.' as a folder`,
		"/leading/":  `ending in '/'`,
		"has space/": `ending in '/'`,
	} {
		t.Run(path, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: protoV6ProviderFactories(),
				Steps: []resource.TestStep{{
					Config: providerConfig(api) + fmt.Sprintf(`
resource "cosmoner_bucket" "assets" {
  name         = "assets"
  region       = "eu-north-1"
  public_paths = [%q]
}
`, path),
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(message),
				}},
			})
		})
	}
}

func TestBucketResourceNameIsUnique(t *testing.T) {
	clearEnv(t)
	api := newFakeAPI(t)
	bucket := func(resourceName string) string {
		return fmt.Sprintf(`
resource "cosmoner_bucket" %q {
  name   = "assets"
  region = "eu-north-1"
}
`, resourceName)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkAPICount(api, 0),
		Steps: []resource.TestStep{
			{Config: providerConfig(api) + bucket("first")},
			{
				Config:      providerConfig(api) + bucket("first") + bucket("second"),
				ExpectError: regexp.MustCompile(`already exists`),
			},
		},
	})
}
