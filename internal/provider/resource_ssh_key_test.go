package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
)

const testPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFakeKeyMaterialForProviderTests me@laptop"

func TestSSHKeyResource(t *testing.T) {
	clearEnv(t)
	api := newFakeAPI(t)
	const name = "cosmoner_ssh_key.laptop"

	config := func(label string) string {
		// Read from a .pub file, a key ends in a newline, and hand-pasted ones
		// pick up stray spaces. The API normalises both away.
		return providerConfig(api) + fmt.Sprintf(`
resource "cosmoner_ssh_key" "laptop" {
  name       = %q
  public_key = "ssh-ed25519   AAAAC3NzaC1lZDI1NTE5AAAAIFakeKeyMaterialForProviderTests me@laptop\n"
}
`, label)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkAPICount(api, 0),
		Steps: []resource.TestStep{
			{
				// The harness re-plans after every apply and fails on a
				// non-empty plan, so this step also proves the normalised key
				// does not show up as a change.
				Config: config("Laptop"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(name, "id"),
					resource.TestCheckResourceAttr(name, "fingerprint", "SHA256:AAAAC3NzaC1lZDI1NTE5AAAAIFakeKeyMaterialForProviderTests"),
					resource.TestCheckResourceAttrSet(name, "created_at"),
				),
			},
			{
				// Keys cannot be edited, so a rename replaces — and the old key
				// goes first, or the API would refuse the duplicate.
				Config: config("Work laptop"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(name, plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check: resource.TestCheckResourceAttr(name, "name", "Work laptop"),
			},
			{
				ResourceName:      name,
				ImportState:       true,
				ImportStateIdFunc: importID(name),
				ImportStateVerify: true,
				// Imported as the API stores it, without the stray whitespace.
				ImportStateVerifyIgnore: []string{"public_key"},
			},
		},
	})
}

func TestSSHKeyResourceDuplicate(t *testing.T) {
	clearEnv(t)
	api := newFakeAPI(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config: providerConfig(api) + fmt.Sprintf(`
resource "cosmoner_ssh_key" "a" {
  name       = "First"
  public_key = %[1]q
}

resource "cosmoner_ssh_key" "b" {
  name       = "Second"
  public_key = %[1]q
  depends_on = [cosmoner_ssh_key.a]
}
`, testPublicKey),
			ExpectError: regexp.MustCompile(`already registered as "First"`),
		}},
	})
}
