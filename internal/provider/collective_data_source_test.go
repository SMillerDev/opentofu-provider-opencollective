// Copyright IBM Corp. 2021, 2026
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

// These tests run against the public Open Collective API and use the
// long-standing "babel" collective as a fixture.
const babelCollectiveID = "dgm9bnk8-0437xqry-ejpvzeol-jdayw5re"

func babelChecks(addr string) []statecheck.StateCheck {
	return []statecheck.StateCheck{
		statecheck.ExpectKnownValue(addr, tfjsonpath.New("id"), knownvalue.StringExact(babelCollectiveID)),
		statecheck.ExpectKnownValue(addr, tfjsonpath.New("slug"), knownvalue.StringExact("babel")),
		statecheck.ExpectKnownValue(addr, tfjsonpath.New("legacy_id"), knownvalue.Int64Exact(580)),
		statecheck.ExpectKnownValue(addr, tfjsonpath.New("name"), knownvalue.StringExact("Babel")),
		statecheck.ExpectKnownValue(addr, tfjsonpath.New("type"), knownvalue.StringExact("COLLECTIVE")),
		statecheck.ExpectKnownValue(addr, tfjsonpath.New("currency"), knownvalue.NotNull()),
		statecheck.ExpectKnownValue(addr, tfjsonpath.New("host_slug"), knownvalue.NotNull()),
		statecheck.ExpectKnownValue(addr, tfjsonpath.New("tags"), knownvalue.NotNull()),
		statecheck.ExpectKnownValue(addr, tfjsonpath.New("social_links"), knownvalue.NotNull()),
	}
}

func TestAccCollectiveDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
data "opencollective_collective" "by_slug" {
  slug = "babel"
}

data "opencollective_collective" "by_id" {
  id = "` + babelCollectiveID + `"
}

data "opencollective_collective" "by_github" {
  github_handle = "babel"
}
`,
				ConfigStateChecks: append(append(
					babelChecks("data.opencollective_collective.by_slug"),
					babelChecks("data.opencollective_collective.by_id")...),
					append(babelChecks("data.opencollective_collective.by_github"),
						statecheck.ExpectKnownValue("data.opencollective_collective.by_github",
							tfjsonpath.New("github_handle"), knownvalue.StringExact("babel")),
					)...,
				),
			},
		},
	})
}

func TestAccCollectiveDataSource_notFound(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
data "opencollective_collective" "test" {
  slug = "this-collective-should-not-exist-0f3a9c"
}
`,
				ExpectError: regexp.MustCompile(`Collective not found`),
			},
		},
	})
}

func TestAccCollectiveDataSource_lookupValidation(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
data "opencollective_collective" "test" {}
`,
				ExpectError: regexp.MustCompile(`Missing Attribute Configuration`),
			},
			{
				Config: `
data "opencollective_collective" "test" {
  slug          = "babel"
  github_handle = "babel"
}
`,
				ExpectError: regexp.MustCompile(`Invalid Attribute Combination`),
			},
		},
	})
}
