package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"
)

func checkSecretValue(api *fakeAPI, name, want string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if got := api.secretValue(name); got != want {
			return fmt.Errorf("secret %s holds %q, want %q", name, got, want)
		}
		return nil
	}
}

func TestSecretResource(t *testing.T) {
	clearEnv(t)
	api := newFakeAPI(t)
	const name = "cosmoner_secret.db"

	config := func(value, description string) string {
		desc := ""
		if description != "" {
			desc = fmt.Sprintf("description = %q", description)
		}
		return providerConfig(api) + fmt.Sprintf(`
resource "cosmoner_secret" "db" {
  name  = "DB_PASSWORD"
  value = %q
  %s
}
`, value, desc)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkAPICount(api, 0),
		Steps: []resource.TestStep{
			{
				Config: config("hunter2", "Primary database"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(name, "id"),
					resource.TestCheckResourceAttr(name, "project_id", testProjectID),
					resource.TestCheckResourceAttr(name, "environment", "default"),
					resource.TestCheckResourceAttr(name, "description", "Primary database"),
					resource.TestCheckResourceAttr(name, "version", "1"),
					resource.TestCheckResourceAttrSet(name, "created_at"),
					resource.TestCheckResourceAttrSet(name, "updated_at"),
					checkSecretValue(api, "DB_PASSWORD", "hunter2"),
				),
			},
			{
				Config: config("correct-horse", "Primary database"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(name, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(name, "version", "2"),
					checkSecretValue(api, "DB_PASSWORD", "correct-horse"),
				),
			},
			{
				// Someone sets the value in the dashboard. The provider cannot
				// read it, but sees the version move and sets it back.
				PreConfig: func() { api.rotateSecretElsewhere("DB_PASSWORD") },
				Config:    config("correct-horse", "Primary database"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(name, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(name, "version", "4"),
					checkSecretValue(api, "DB_PASSWORD", "correct-horse"),
				),
			},
			{
				// Removing the description from configuration clears it.
				Config: config("correct-horse", ""),
				Check:  resource.TestCheckNoResourceAttr(name, "description"),
			},
			{
				ResourceName:      name,
				ImportState:       true,
				ImportStateIdFunc: importID(name),
				ImportStateVerify: true,
				// The API never returns it, so an import cannot know it.
				ImportStateVerifyIgnore: []string{"value"},
			},
			{
				// A bare id works when the provider has a default project.
				ResourceName:            name,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"value"},
			},
		},
	})
}

func TestSecretResourceWriteOnlyValue(t *testing.T) {
	clearEnv(t)
	api := newFakeAPI(t)
	const name = "cosmoner_secret.token"

	config := func(value string, version int) string {
		return providerConfig(api) + fmt.Sprintf(`
resource "cosmoner_secret" "token" {
  name             = "API_TOKEN"
  environment      = "production"
  value_wo         = %q
  value_wo_version = %d
}
`, value, version)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_11_0)},
		CheckDestroy:             checkAPICount(api, 0),
		Steps: []resource.TestStep{
			{
				Config: config("first", 1),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(name, "environment", "production"),
					resource.TestCheckNoResourceAttr(name, "value"),
					resource.TestCheckNoResourceAttr(name, "value_wo"),
					resource.TestCheckResourceAttr(name, "version", "1"),
					checkSecretValue(api, "API_TOKEN", "first"),
				),
			},
			{
				// Terraform cannot see a write-only value change on its own.
				Config: config("second", 1),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: checkSecretValue(api, "API_TOKEN", "first"),
			},
			{
				Config: config("second", 2),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(name, "version", "2"),
					checkSecretValue(api, "API_TOKEN", "second"),
				),
			},
			{
				PreConfig: func() { api.rotateSecretElsewhere("API_TOKEN") },
				Config:    config("second", 2),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(name, plancheck.ResourceActionUpdate)},
				},
				Check: checkSecretValue(api, "API_TOKEN", "second"),
			},
		},
	})
}

func TestSecretResourceInAnotherProject(t *testing.T) {
	clearEnv(t)
	api := newFakeAPI(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		CheckDestroy:             checkAPICount(api, 0),
		Steps: []resource.TestStep{{
			Config: providerConfig(api) + `
resource "cosmoner_secret" "other" {
  project_id = "proj_other"
  name       = "SHARED"
  value      = "x"
}
`,
			Check: resource.TestCheckResourceAttr("cosmoner_secret.other", "project_id", otherProjectID),
		}},
	})
}

func TestSecretResourceValidation(t *testing.T) {
	clearEnv(t)
	api := newFakeAPI(t)

	cases := map[string]struct {
		body string
		err  string
		// writeOnly cases need a Terraform that understands value_wo at all.
		writeOnly bool
	}{
		"no value":        {body: `name = "A"`, err: `Exactly one of these attributes must be configured: \[value,value_wo\]`},
		"both values":     {body: "name = \"A\"\nvalue = \"x\"\nvalue_wo = \"y\"\nvalue_wo_version = 1", err: `Exactly one of these attributes must be configured`, writeOnly: true},
		"no wo version":   {body: "name = \"A\"\nvalue_wo = \"y\"", err: `Attribute "value_wo_version" must be specified when "value_wo" is\s+specified`, writeOnly: true},
		"lowercase name":  {body: "name = \"db_password\"\nvalue = \"x\"", err: `must be uppercase letters`},
		"empty value":     {body: "name = \"A\"\nvalue = \"\"", err: `string length must be between 1 and 10000`},
		"too long a name": {body: fmt.Sprintf("name = \"A%0100d\"\nvalue = \"x\"", 0), err: `string length must be at most 100`},
	}
	for label, tc := range cases {
		t.Run(label, func(t *testing.T) {
			var checks []tfversion.TerraformVersionCheck
			if tc.writeOnly {
				checks = append(checks, tfversion.SkipBelow(tfversion.Version1_11_0))
			}
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: protoV6ProviderFactories(),
				TerraformVersionChecks:   checks,
				Steps: []resource.TestStep{{
					Config:      providerConfig(api) + fmt.Sprintf("resource \"cosmoner_secret\" \"bad\" {\n%s\n}\n", tc.body),
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(tc.err),
				}},
			})
		})
	}
}

// A Terraform without write-only support would otherwise treat value_wo as an
// ordinary argument and write the plaintext into state, which is exactly what
// the attribute exists to prevent.
func TestSecretResourceWriteOnlyNeedsANewTerraform(t *testing.T) {
	clearEnv(t)
	api := newFakeAPI(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipAbove(tfversion.Version1_10_0)},
		CheckDestroy:             checkAPICount(api, 0),
		Steps: []resource.TestStep{{
			Config: providerConfig(api) + `
resource "cosmoner_secret" "token" {
  name             = "API_TOKEN"
  value_wo         = "plaintext"
  value_wo_version = 1
}
`,
			ExpectError: regexp.MustCompile(`(?i)write-only`),
		}},
	})
}

func TestSecretResourceDuplicateName(t *testing.T) {
	clearEnv(t)
	api := newFakeAPI(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories(),
		Steps: []resource.TestStep{{
			Config: providerConfig(api) + `
resource "cosmoner_secret" "a" {
  name  = "DUPLICATE"
  value = "x"
}

resource "cosmoner_secret" "b" {
  name       = "DUPLICATE"
  value      = "y"
  depends_on = [cosmoner_secret.a]
}
`,
			ExpectError: regexp.MustCompile(`already exists in the\s+default environment`),
		}},
	})
}
