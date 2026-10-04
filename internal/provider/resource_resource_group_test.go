package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
)

func TestResourceGroupResource(t *testing.T) {
	clearEnv(t)
	api := newFakeAPI(t)
	const parent, child = "cosmoner_resource_group.production", "cosmoner_resource_group.backend"

	nested := providerConfig(api) + `
resource "cosmoner_resource_group" "production" {
  name = "Production"
}

resource "cosmoner_resource_group" "backend" {
  name      = "Backend"
  parent_id = cosmoner_resource_group.production.id
}
`
	topLevel := providerConfig(api) + `
resource "cosmoner_resource_group" "production" {
  name = "Production"
}

resource "cosmoner_resource_group" "backend" {
  name = " Backend services "
}
`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkAPICount(api, 0),
		Steps: []resource.TestStep{
			{
				Config: nested,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(parent, "id"),
					resource.TestCheckNoResourceAttr(parent, "parent_id"),
					resource.TestCheckResourceAttrPair(child, "parent_id", parent, "id"),
				),
			},
			{
				// Moved to the top level and renamed in place. The API trims the
				// name; the configured spelling is kept, so there is no diff after.
				Config: topLevel,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(child, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr(child, "parent_id"),
					resource.TestCheckResourceAttr(child, "name", " Backend services "),
				),
			},
			{
				ResourceName:      parent,
				ImportState:       true,
				ImportStateIdFunc: importID(parent),
				ImportStateVerify: true,
			},
		},
	})
}
