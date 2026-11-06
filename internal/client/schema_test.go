// Copyright IBM Corp. 2021, 2026
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"errors"
	"os"
	"testing"
)

// TestOperationsMatchLiveSchema sends every operation to the live API with
// placeholder variables and fails if the server reports a parse or validation
// error. Authorisation and "not found" errors are expected and ignored, so no
// token is needed and nothing is modified.
//
// Run with OPENCOLLECTIVE_SCHEMA_CHECK=1. Unauthenticated callers are limited
// to 10 requests per minute, so this test may wait on the rate limiter.
func TestOperationsMatchLiveSchema(t *testing.T) {
	if os.Getenv("OPENCOLLECTIVE_SCHEMA_CHECK") == "" {
		t.Skip("set OPENCOLLECTIVE_SCHEMA_CHECK=1 to validate operations against the live API")
	}

	const missingID = "00000000-00000000-00000000-00000000"
	const babelID = "dgm9bnk8-0437xqry-ejpvzeol-jdayw5re"
	desc, since := "x", "2024-01-01T00:00:00Z"
	c := New(os.Getenv("OPENCOLLECTIVE_ENDPOINT"))
	ctx := context.Background()

	ops := map[string]func() error{
		"GetCollective": func() error {
			_, err := c.GetCollective(ctx, CollectiveLookup{Slug: "babel"})
			return err
		},
		"CreateCollective": func() error {
			_, err := c.CreateCollective(ctx, CollectiveCreateInput{Name: "x", Slug: "x", Description: "x", Tags: []string{"x"}}, "opensource")
			return err
		},
		"EditCollective": func() error {
			_, err := c.UpdateCollective(ctx, CollectiveUpdateInput{
				ID: missingID, Name: "x", Slug: "x", Description: "x", LongDescription: "x",
				Tags: []string{"x"}, SocialLinks: []SocialLink{{Type: "GITHUB", URL: "https://github.com/x"}}, Currency: "USD",
			})
			return err
		},
		"ApplyToHost":    func() error { return c.ApplyToHost(ctx, missingID, "opensource") },
		"RemoveHost":     func() error { return c.RemoveHost(ctx, missingID) },
		"DeleteAccount":  func() error { return c.DeleteAccount(ctx, missingID) },
		"ArchiveAccount": func() error { return c.ArchiveAccount(ctx, missingID) },
		"GetMemberships": func() error {
			ms, err := c.GetMemberships(ctx, babelID, AccountReference{Slug: "loganfsmyth"})
			if err == nil && (len(ms) == 0 || ms[0].Status != MembershipActive || ms[0].Role != "MEMBER" || ms[0].MemberAccount.ID == "") {
				t.Errorf("GetMemberships = %+v, want an ACTIVE MEMBER membership", ms)
			}
			return err
		},
		"InviteMember": func() error {
			_, err := c.InviteMember(ctx, MemberInput{CollectiveID: missingID, Member: AccountReference{Slug: "x"}, Role: "ADMIN", Description: &desc, Since: &since})
			return err
		},
		"EditMember": func() error {
			return c.EditMembership(ctx, MembershipActive, MemberInput{CollectiveID: missingID, Member: AccountReference{ID: missingID}, Role: "MEMBER", Description: &desc, Since: &since})
		},
		"EditMemberInvitation": func() error {
			return c.EditMembership(ctx, MembershipInvited, MemberInput{CollectiveID: missingID, Member: AccountReference{ID: missingID}, Role: "MEMBER", Description: &desc, Since: &since})
		},
		"RemoveMember": func() error {
			return c.RemoveMembership(ctx, MembershipInvited, missingID, AccountReference{ID: missingID}, "ADMIN")
		},
	}

	for name, op := range ops {
		t.Run(name, func(t *testing.T) {
			err := op()

			var gqlErrs Errors
			if !errors.As(err, &gqlErrs) {
				if err != nil {
					t.Fatalf("unexpected non-GraphQL error: %v", err)
				}
				return
			}

			for _, e := range gqlErrs {
				switch e.Extensions["code"] {
				case "GRAPHQL_PARSE_FAILED", "GRAPHQL_VALIDATION_FAILED", "BAD_USER_INPUT":
					t.Errorf("schema error: %s", e.Message)
				default:
					t.Logf("expected runtime error: %s (%v)", e.Message, e.Extensions["code"])
				}
			}
		})
	}
}
