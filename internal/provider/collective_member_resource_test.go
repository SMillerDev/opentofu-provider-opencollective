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

const memberAddr = "opencollective_collective_member.test"

func expectMember(attr string, v knownvalue.Check) statecheck.StateCheck {
	return statecheck.ExpectKnownValue(memberAddr, tfjsonpath.New(attr), v)
}

func expectAction(action plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{
		PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(memberAddr, action)},
	}
}

// memberConfig returns a collective plus a membership with the given attributes.
func memberConfig(api *fakeAPI, memberAttrs string) string {
	return api.providerConfig() + `
resource "opencollective_collective" "test" {
  name        = "Team Project"
  slug        = "team-project"
  description = "Has a team"
}
` + collectiveMemberBlock(memberAttrs)
}

func collectiveMemberBlock(attrs string) string {
	return `
resource "opencollective_collective_member" "test" {
  collective_id = opencollective_collective.test.id
  member_slug   = "jane"
` + attrs + `
}
`
}

// checkNoMembers verifies destroy removed the membership or invitation.
func checkNoMembers(api *fakeAPI) resource.TestCheckFunc {
	return func(*terraform.State) error {
		api.mu.Lock()
		defer api.mu.Unlock()

		for _, ms := range api.members {
			for _, m := range ms {
				if m.account.Slug == "jane" {
					return fmt.Errorf("membership of jane still exists after destroy (%s)", m.status)
				}
			}
		}

		return nil
	}
}

func TestAccCollectiveMemberResource(t *testing.T) {
	api := newFakeAPI(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkNoMembers(api),
		Steps: []resource.TestStep{
			// Invite. The API normalises since to millisecond precision;
			// the configured form must be kept.
			{
				Config: memberConfig(api, `
  role        = "ADMIN"
  description = "Maintainer"
  since       = "2024-01-01T00:00:00Z"
`),
				ConfigStateChecks: []statecheck.StateCheck{
					expectMember("status", knownvalue.StringExact("INVITED")),
					expectMember("member_account_id", knownvalue.StringExact("acct-jane")),
					expectMember("id", knownvalue.StringExact("fake-0001/acct-jane")),
					expectMember("role", knownvalue.StringExact("ADMIN")),
					expectMember("description", knownvalue.StringExact("Maintainer")),
					expectMember("since", knownvalue.StringExact("2024-01-01T00:00:00Z")),
				},
			},
			// Edit the pending invitation in place.
			{
				Config: memberConfig(api, `
  role        = "MEMBER"
  description = "Core contributor"
  since       = "2024-01-01T00:00:00Z"
`),
				ConfigPlanChecks: expectAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					expectMember("status", knownvalue.StringExact("INVITED")),
					expectMember("role", knownvalue.StringExact("MEMBER")),
					expectMember("description", knownvalue.StringExact("Core contributor")),
				},
			},
			// The invitation is accepted: only computed values change.
			{
				PreConfig: func() { api.acceptInvitation("jane") },
				Config: memberConfig(api, `
  role        = "MEMBER"
  description = "Core contributor"
  since       = "2024-01-01T00:00:00Z"
`),
				ConfigPlanChecks: expectAction(plancheck.ResourceActionNoop),
				ConfigStateChecks: []statecheck.StateCheck{
					expectMember("status", knownvalue.StringExact("ACTIVE")),
				},
			},
			// Edit the accepted membership: change role, clear description.
			{
				Config: memberConfig(api, `
  role  = "ACCOUNTANT"
  since = "2024-01-01T00:00:00Z"
`),
				ConfigPlanChecks: expectAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					expectMember("status", knownvalue.StringExact("ACTIVE")),
					expectMember("role", knownvalue.StringExact("ACCOUNTANT")),
					expectMember("description", knownvalue.Null()),
				},
			},
			// Import by collective slug and by collective ID. since comes back
			// in the API's format, which is the same instant.
			{
				ResourceName:            memberAddr,
				ImportState:             true,
				ImportStateId:           "team-project/jane",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"since"},
			},
			{
				ResourceName:            memberAddr,
				ImportState:             true,
				ImportStateId:           "fake-0001/jane",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"since"},
			},
		},
	})

	for op, want := range map[string]int{"InviteMember": 1, "EditMemberInvitation": 1, "EditMember": 1, "RemoveMember": 1} {
		if n := api.called(op); n != want {
			t.Errorf("%s called %d times, want %d", op, n, want)
		}
	}
}

func TestAccCollectiveMemberResource_destroyPendingInvitation(t *testing.T) {
	api := newFakeAPI(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkNoMembers(api),
		Steps: []resource.TestStep{{
			Config: memberConfig(api, `role = "ADMIN"`),
			ConfigStateChecks: []statecheck.StateCheck{
				expectMember("status", knownvalue.StringExact("INVITED")),
				expectMember("description", knownvalue.Null()),
				expectMember("since", knownvalue.Null()),
			},
		}},
	})
}

func TestAccCollectiveMemberResource_sinceDefaultsOnAccept(t *testing.T) {
	api := newFakeAPI(t)
	config := memberConfig(api, `role = "MEMBER"`)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: config},
			{
				PreConfig:        func() { api.acceptInvitation("jane") },
				Config:           config,
				ConfigPlanChecks: expectAction(plancheck.ResourceActionNoop),
				ConfigStateChecks: []statecheck.StateCheck{
					expectMember("status", knownvalue.StringExact("ACTIVE")),
					expectMember("since", knownvalue.StringExact("2026-02-01T00:00:00.000Z")),
				},
			},
		},
	})
}

func TestAccCollectiveMemberResource_drift(t *testing.T) {
	api := newFakeAPI(t)
	config := memberConfig(api, `role = "ADMIN"`)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: config},
			// Accepted, then the role is changed in the UI: restored in place.
			{
				PreConfig: func() {
					api.acceptInvitation("jane")
					api.setRoleOutOfBand("jane", "MEMBER")
				},
				Config:           config,
				ConfigPlanChecks: expectAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					expectMember("role", knownvalue.StringExact("ADMIN")),
				},
			},
			// Member renamed their account: tracked by ID, no replacement.
			{
				PreConfig:        func() { api.renameAccount("jane", "jane-smith") },
				Config:           config,
				ConfigPlanChecks: expectAction(plancheck.ResourceActionNoop),
				ConfigStateChecks: []statecheck.StateCheck{
					expectMember("member_slug", knownvalue.StringExact("jane")),
					expectMember("status", knownvalue.StringExact("ACTIVE")),
				},
			},
		},
	})
}

func TestAccCollectiveMemberResource_removedOutOfBand(t *testing.T) {
	api := newFakeAPI(t)
	config := memberConfig(api, `role = "ADMIN"`)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: config},
			// Invitation declined: a new one is sent.
			{
				PreConfig:        func() { api.removeMemberOutOfBand("jane") },
				Config:           config,
				ConfigPlanChecks: expectAction(plancheck.ResourceActionCreate),
				ConfigStateChecks: []statecheck.StateCheck{
					expectMember("status", knownvalue.StringExact("INVITED")),
				},
			},
		},
	})

	if n := api.called("InviteMember"); n != 2 {
		t.Errorf("InviteMember called %d times, want 2", n)
	}
}

func TestAccCollectiveMemberResource_pagination(t *testing.T) {
	api := newFakeAPI(t)
	collectiveOnly := api.providerConfig() + `
resource "opencollective_collective" "test" {
  name        = "Team Project"
  slug        = "team-project"
  description = "Has a team"
}
`
	withMember := collectiveOnly + collectiveMemberBlock(`role = "ADMIN"`)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: collectiveOnly},
			// 250 members join before jane, so she is on the third page.
			{
				PreConfig: func() { api.addActiveMembers("team-project", 250) },
				Config:    withMember,
			},
			{
				PreConfig:        func() { api.acceptInvitation("jane") },
				Config:           withMember,
				ConfigPlanChecks: expectAction(plancheck.ResourceActionNoop),
				ConfigStateChecks: []statecheck.StateCheck{
					expectMember("status", knownvalue.StringExact("ACTIVE")),
				},
			},
		},
	})
}

func TestAccCollectiveMemberResource_validation(t *testing.T) {
	api := newFakeAPI(t)

	cases := map[string]struct {
		attrs string
		want  string
	}{
		"platform role": {`role = "BACKER"`, `value must be one of`},
		"bad since":     {"role = \"ADMIN\"\n  since = \"2024-01-01\"", `RFC 3339`},
		"empty desc":    {"role = \"ADMIN\"\n  description = \"\"", `string length must be at least 1`},
		"empty member":  {`role = "ADMIN"`, `string length must be at least 1`},
	}

	for name, tc := range cases {
		config := memberConfig(api, tc.attrs)
		if name == "empty member" {
			config = regexp.MustCompile(`member_slug\s+= "jane"`).ReplaceAllString(config, `member_slug = ""`)
		}

		t.Run(name, func(t *testing.T) {
			resource.Test(t, resource.TestCase{
				PreCheck:                 func() { testAccPreCheck(t) },
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{{
					Config:      config,
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(tc.want),
				}},
			})
		})
	}

	if n := api.called("InviteMember"); n != 0 {
		t.Errorf("InviteMember called %d times, want 0", n)
	}
}

func TestAccCollectiveMemberResource_importErrors(t *testing.T) {
	api := newFakeAPI(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: memberConfig(api, `role = "ADMIN"`)},
			{
				ResourceName:  memberAddr,
				ImportState:   true,
				ImportStateId: "team-project",
				ExpectError:   regexp.MustCompile(`Expected <collective id or slug>/<member slug>`),
			},
			{
				ResourceName:  memberAddr,
				ImportState:   true,
				ImportStateId: "team-project/nobody",
				ExpectError:   regexp.MustCompile(`"nobody" is not a member of`),
			},
			{
				ResourceName:  memberAddr,
				ImportState:   true,
				ImportStateId: "no-such-collective/jane",
				ExpectError:   regexp.MustCompile(`No collective has ID or slug`),
			},
		},
	})
}
