package provider

import (
	"time"

	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

// The fake moves a resource to ready after a couple of reads, so waiting the
// real interval between them would only slow the tests down.
func init() {
	pollInterval = 10 * time.Millisecond
}

func tfjsonPath(attribute string) tfjsonpath.Path {
	return tfjsonpath.New(attribute)
}

func knownString(value string) knownvalue.Check {
	return knownvalue.StringExact(value)
}
