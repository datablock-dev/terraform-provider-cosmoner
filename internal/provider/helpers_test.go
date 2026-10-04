package provider

import (
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

func tfjsonPath(attribute string) tfjsonpath.Path {
	return tfjsonpath.New(attribute)
}

func knownString(value string) knownvalue.Check {
	return knownvalue.StringExact(value)
}
