package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestPostgresDatabaseResource(t *testing.T) {
	clearEnv(t)
	api := newFakeAPI(t)
	const name = "cosmoner_postgres_database.main"

	config := func(size string) string {
		return providerConfig(api) + fmt.Sprintf(`
resource "cosmoner_postgres_database" "main" {
  name    = "main"
  size    = %q
  version = "17"
  region  = "se-sto"
}
`, size)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkAPICount(api, 0),
		Steps: []resource.TestStep{
			{
				// The create answer has no id; the provider finds the database
				// by listing, and waits for it to come online.
				Config: config("pg-small"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(name, "id"),
					resource.TestCheckResourceAttr(name, "status", "ONLINE"),
					resource.TestCheckResourceAttr(name, "port", "5432"),
					resource.TestCheckResourceAttr(name, "database", "app"),
					resource.TestCheckResourceAttr(name, "username", "app"),
					resource.TestCheckResourceAttrSet(name, "host"),
					resource.TestCheckResourceAttrSet(name, "public_host"),
					resource.TestMatchResourceAttr(name, "connection_uri", regexp.MustCompile(`^postgresql://app:secret@`)),
					resource.TestMatchResourceAttr(name, "public_connection_uri", regexp.MustCompile(`sslmode=require$`)),
				),
			},
			{
				Config: config("pg-small"),
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
				// There is no resize route.
				Config: config("pg-medium"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(name, plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(name, "size", "pg-medium"),
					checkAPICount(api, 1),
				),
			},
		},
	})
}

func TestPostgresDatabaseResourceTellsSameNamedDatabasesApart(t *testing.T) {
	clearEnv(t)
	api := newFakeAPI(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkAPICount(api, 0),
		Steps: []resource.TestStep{{
			// Names are not unique, so two databases called "shared" in one
			// project each have to end up with their own id.
			Config: providerConfig(api) + `
resource "cosmoner_postgres_database" "a" {
  name    = "shared"
  size    = "pg-small"
  version = "17"
  region  = "se-sto"
}

resource "cosmoner_postgres_database" "b" {
  name    = "shared"
  size    = "pg-small"
  version = "17"
  region  = "se-sto"

  depends_on = [cosmoner_postgres_database.a]
}
`,
			Check: func(s *terraform.State) error {
				a := s.RootModule().Resources["cosmoner_postgres_database.a"].Primary.ID
				b := s.RootModule().Resources["cosmoner_postgres_database.b"].Primary.ID
				if a == b {
					return fmt.Errorf("both resources hold database %s", a)
				}
				if n := api.databaseCount("shared"); n != 2 {
					return fmt.Errorf("fake API holds %d databases named shared, want 2", n)
				}
				return nil
			},
		}},
	})
}

func TestPostgresDatabaseResourceRefusedCreateLeavesNothing(t *testing.T) {
	clearEnv(t)
	api := newFakeAPI(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkAPICount(api, 0),
		Steps: []resource.TestStep{{
			Config: providerConfig(api) + `
resource "cosmoner_postgres_database" "main" {
  name    = "main"
  size    = "pg-small"
  version = "17"
  region  = "eu-west"
}
`,
			ExpectError: regexp.MustCompile(`not available in the selected region`),
		}},
	})
}

func TestPostgresDatabaseResourceFailedProvisionIsTainted(t *testing.T) {
	clearEnv(t)
	api := newFakeAPI(t)
	const name = "cosmoner_postgres_database.main"
	config := providerConfig(api) + `
resource "cosmoner_postgres_database" "main" {
  name    = "broken-db"
  size    = "pg-small"
  version = "17"
  region  = "se-sto"
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkAPICount(api, 0),
		Steps: []resource.TestStep{
			{
				Config:      config,
				ExpectError: regexp.MustCompile(`(?s)The database was created but is not ready.*status ERROR.*tainted`),
			},
			{
				// The next apply replaces the tainted one (which here fails the
				// same way again); CheckDestroy then proves neither is left.
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(name, plancheck.ResourceActionReplace)},
				},
				ExpectError: regexp.MustCompile(`(?s)The database was created but is not ready.*status ERROR.*tainted`),
			},
		},
	})
}

func TestPostgresDatabaseResourceAdoptsADatabaseWhoseCreateAnswerWasLost(t *testing.T) {
	clearEnv(t)
	api := newFakeAPI(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkAPICount(api, 0),
		Steps: []resource.TestStep{{
			// The API creates the database and then answers 502. Reporting
			// only the error would leave a billed database outside state.
			Config: providerConfig(api) + `
resource "cosmoner_postgres_database" "main" {
  name    = "flaky-db"
  size    = "pg-small"
  version = "17"
  region  = "se-sto"
}
`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("cosmoner_postgres_database.main", "status", "ONLINE"),
				func(*terraform.State) error {
					if n := api.databaseCount("flaky-db"); n != 1 {
						return fmt.Errorf("fake API holds %d databases named flaky-db, want 1", n)
					}
					return nil
				},
			),
		}},
	})
}
