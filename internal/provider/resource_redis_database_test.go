package provider

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
)

func TestRedisDatabaseResource(t *testing.T) {
	clearEnv(t)
	api := newFakeAPI(t)
	const name = "cosmoner_redis_database.cache"
	config := providerConfig(api) + `
resource "cosmoner_redis_database" "cache" {
  name   = "cache"
  plan   = "valkey-1gb"
  region = "se-sto"
}
`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkAPICount(api, 0),
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(name, "id"),
					resource.TestCheckResourceAttr(name, "status", "ACTIVE"),
					resource.TestCheckResourceAttr(name, "persistence", "NONE"),
					resource.TestCheckResourceAttr(name, "engine", "VALKEY"),
					resource.TestCheckResourceAttr(name, "memory_mb", "1024"),
					resource.TestCheckResourceAttr(name, "port", "6379"),
					resource.TestCheckResourceAttr(name, "password", "redis-secret"),
				),
			},
			{
				Config: config,
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
		},
	})
}

func TestRedisDatabaseResourceTimeoutIsTainted(t *testing.T) {
	clearEnv(t)
	api := newFakeAPI(t)
	const name = "cosmoner_redis_database.cache"
	config := providerConfig(api) + `
resource "cosmoner_redis_database" "cache" {
  name   = "stuck-cache"
  plan   = "valkey-1gb"
  region = "se-sto"

  timeouts {
    create = "1s"
  }
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkAPICount(api, 0),
		Steps: []resource.TestStep{
			{
				Config:      config,
				ExpectError: regexp.MustCompile(`(?s)did\s+not\s+become\s+ready:\s+still\s+not\s+finished\s+after\s+1s`),
			},
			{
				// The next apply replaces the tainted one (which here fails the
				// same way again); CheckDestroy then proves neither is left.
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(name, plancheck.ResourceActionReplace)},
				},
				ExpectError: regexp.MustCompile(`(?s)did\s+not\s+become\s+ready:\s+still\s+not\s+finished\s+after\s+1s`),
			},
		},
	})
}

func TestRedisDatabaseResourceRefusedPlan(t *testing.T) {
	clearEnv(t)
	api := newFakeAPI(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkAPICount(api, 0),
		Steps: []resource.TestStep{{
			Config: providerConfig(api) + `
resource "cosmoner_redis_database" "cache" {
  name   = "cache"
  plan   = "redis-ram-1gb"
  region = "se-sto"
}
`,
			ExpectError: regexp.MustCompile(`This plan is no longer available`),
		}},
	})
}
