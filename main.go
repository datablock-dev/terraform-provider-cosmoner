// Command terraform-provider-cosmoner is the Cosmoner Terraform provider.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/datablock-dev/terraform-provider-cosmoner/internal/provider"
)

// Regenerates docs/ from the schema and examples/. CI fails when the committed
// copy is stale, because docs/ is what the Terraform Registry publishes.
//go:generate go tool tfplugindocs generate --provider-name cosmoner

// version is set at build time by the release, through -ldflags.
var version = "dev"

// registryAddress is where Terraform finds the provider. It has to match the
// `source` users write in required_providers, and the Registry derives it from
// the GitHub organization that publishes the release.
const registryAddress = "registry.terraform.io/datablock-dev/cosmoner"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run the provider with support for debuggers like delve")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: registryAddress,
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err)
	}
}
