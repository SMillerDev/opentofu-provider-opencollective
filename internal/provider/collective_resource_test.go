// Copyright IBM Corp. 2021, 2026
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

const collectiveAddr = "opencollective_collective.test"

func expect(attr string, v knownvalue.Check) statecheck.StateCheck {
	return statecheck.ExpectKnownValue(collectiveAddr, tfjsonpath.New(attr), v)
}

// These tests run OpenTofu/Terraform against an in-process fake of the Open
// Collective API, so they need TF_ACC but no credentials.
func TestAccCollectiveResource(t *testing.T) {
	api := newFakeAPI(t)
	initialConfig := api.providerConfig() + `
resource "opencollective_collective" "test" {
  name        = "My Project"
  slug        = "my-project"
  description = "Building things"
  host_slug   = "opensource"
}
`

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			if n := api.called("DeleteAccount"); n != 1 {
				return fmt.Errorf("DeleteAccount called %d times, want 1", n)
			}
			if n := api.called("ArchiveAccount"); n != 0 {
				return fmt.Errorf("ArchiveAccount called %d times, want 0", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			// Create with only the required attributes and a host. The
			// application is still pending, which must not cause a diff.
			{
				Config: initialConfig,
				ConfigStateChecks: []statecheck.StateCheck{
					expect("id", knownvalue.StringExact("fake-0001")),
					expect("legacy_id", knownvalue.Int64Exact(1001)),
					expect("type", knownvalue.StringExact("COLLECTIVE")),
					expect("currency", knownvalue.StringExact("USD")),
					expect("host_slug", knownvalue.StringExact("opensource")),
					expect("host_application_status", knownvalue.StringExact("PENDING")),
					expect("is_approved", knownvalue.Bool(false)),
					expect("long_description", knownvalue.Null()),
					expect("tags", knownvalue.Null()),
					expect("social_links", knownvalue.Null()),
					expect("archive_on_destroy", knownvalue.Bool(false)),
				},
			},
			// The host approves out of band: computed values change, but
			// nothing the user configured does.
			{
				PreConfig: func() { api.approveHost("my-project") },
				Config:    initialConfig,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(collectiveAddr, plancheck.ResourceActionNoop),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					expect("host_slug", knownvalue.StringExact("opensource")),
					expect("host_application_status", knownvalue.StringExact("APPROVED")),
					expect("is_approved", knownvalue.Bool(true)),
				},
			},
			// Update every editable attribute in place and move to another host.
			{
				Config: api.providerConfig() + `
resource "opencollective_collective" "test" {
  name             = "My Renamed Project"
  slug             = "my-renamed-project"
  description      = "Building better things"
  long_description = "<p>Longer story</p>"
  tags             = ["go", "opentofu"]
  currency         = "EUR"
  host_slug        = "europe"

  social_links = [
    { type = "WEBSITE", url = "https://example.com" },
    { type = "GITHUB", url = "https://github.com/example" },
  ]
}
`,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(collectiveAddr, plancheck.ResourceActionUpdate),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					expect("id", knownvalue.StringExact("fake-0001")),
					expect("slug", knownvalue.StringExact("my-renamed-project")),
					expect("name", knownvalue.StringExact("My Renamed Project")),
					expect("long_description", knownvalue.StringExact("<p>Longer story</p>")),
					expect("tags", knownvalue.SetExact([]knownvalue.Check{knownvalue.StringExact("go"), knownvalue.StringExact("opentofu")})),
					expect("currency", knownvalue.StringExact("EUR")),
					expect("website", knownvalue.StringExact("https://example.com")),
					expect("host_slug", knownvalue.StringExact("europe")),
					expect("host_application_status", knownvalue.StringExact("PENDING")),
					expect("social_links", knownvalue.ListExact([]knownvalue.Check{
						knownvalue.ObjectExact(map[string]knownvalue.Check{"type": knownvalue.StringExact("WEBSITE"), "url": knownvalue.StringExact("https://example.com")}),
						knownvalue.ObjectExact(map[string]knownvalue.Check{"type": knownvalue.StringExact("GITHUB"), "url": knownvalue.StringExact("https://github.com/example")}),
					})),
				},
			},
			// Import by ID and by slug.
			{
				ResourceName:      collectiveAddr,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				ResourceName:      collectiveAddr,
				ImportState:       true,
				ImportStateId:     "my-renamed-project",
				ImportStateVerify: true,
			},
			// Remove optional attributes and the host again.
			{
				Config: api.providerConfig() + `
resource "opencollective_collective" "test" {
  name        = "My Renamed Project"
  slug        = "my-renamed-project"
  description = "Building better things"
}
`,
				ConfigStateChecks: []statecheck.StateCheck{
					expect("long_description", knownvalue.Null()),
					expect("tags", knownvalue.Null()),
					expect("social_links", knownvalue.Null()),
					expect("host_slug", knownvalue.Null()),
					expect("host_application_status", knownvalue.Null()),
					expect("currency", knownvalue.StringExact("EUR")),
				},
			},
		},
	})

	if n := api.called("RemoveHost"); n != 2 {
		t.Errorf("RemoveHost called %d times, want 2", n)
	}
	if n := api.called("ApplyToHost"); n != 1 {
		t.Errorf("ApplyToHost called %d times, want 1", n)
	}
}

func TestAccCollectiveResource_allAttributesOnCreate(t *testing.T) {
	api := newFakeAPI(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: api.providerConfig() + `
resource "opencollective_collective" "test" {
  name             = "Full"
  slug             = "full"
  description      = "All attributes at once"
  long_description = "<p>Everything</p>"
  tags             = ["a"]
  currency         = "GBP"

  social_links = [{ type = "GITHUB", url = "https://github.com/full" }]
}
`,
				ConfigStateChecks: []statecheck.StateCheck{
					expect("long_description", knownvalue.StringExact("<p>Everything</p>")),
					expect("currency", knownvalue.StringExact("GBP")),
					expect("social_links", knownvalue.ListSizeExact(1)),
					expect("host_slug", knownvalue.Null()),
				},
			},
		},
	})

	if n := api.called("EditCollective"); n != 1 {
		t.Errorf("EditCollective called %d times, want 1 (follow-up after create)", n)
	}
}

func TestAccCollectiveResource_archiveOnDestroy(t *testing.T) {
	api := newFakeAPI(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			if n := api.called("ArchiveAccount"); n != 1 {
				return fmt.Errorf("ArchiveAccount called %d times, want 1", n)
			}
			if n := api.called("DeleteAccount"); n != 0 {
				return fmt.Errorf("DeleteAccount called %d times, want 0", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: api.providerConfig() + `
resource "opencollective_collective" "test" {
  name               = "Keep Me"
  slug               = "keep-me"
  description        = "Archived, not deleted"
  archive_on_destroy = true
}
`,
				ConfigStateChecks: []statecheck.StateCheck{
					expect("archive_on_destroy", knownvalue.Bool(true)),
					// No follow-up edit is needed for create-only attributes.
					expect("is_archived", knownvalue.Bool(false)),
				},
			},
		},
	})

	if n := api.called("EditCollective"); n != 0 {
		t.Errorf("EditCollective called %d times, want 0", n)
	}
}

func TestAccCollectiveResource_deletedOutOfBand(t *testing.T) {
	api := newFakeAPI(t)
	config := api.providerConfig() + `
resource "opencollective_collective" "test" {
  name        = "Ephemeral"
  slug        = "ephemeral"
  description = "Gone soon"
}
`

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: config},
			{
				PreConfig: func() { api.deleteOutOfBand("ephemeral") },
				Config:    config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(collectiveAddr, plancheck.ResourceActionCreate),
					},
				},
			},
		},
	})
}

func TestAccCollectiveResource_validation(t *testing.T) {
	api := newFakeAPI(t)

	cases := map[string]struct {
		attrs string
		want  string
	}{
		"empty tags":        {`tags = []`, `set must contain at least 1`},
		"empty tag":         {`tags = [""]`, `string length must be at least 1`},
		"empty long desc":   {`long_description = ""`, `string length must be at least 1`},
		"unknown link type": {`social_links = [{ type = "MYSPACE", url = "https://myspace.com/x" }]`, `value must be one of`},
		"empty links":       {`social_links = []`, `list must contain at least 1`},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			resource.Test(t, resource.TestCase{
				PreCheck:                 func() { testAccPreCheck(t) },
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{{
					Config: api.providerConfig() + `
resource "opencollective_collective" "test" {
  name        = "x"
  slug        = "x"
  description = "x"
  ` + tc.attrs + `
}
`,
					ExpectError: regexp.MustCompile(`(?i)` + tc.want),
				}},
			})
		})
	}

	if n := api.called("CreateCollective"); n != 0 {
		t.Errorf("CreateCollective called %d times, want 0", n)
	}
}
