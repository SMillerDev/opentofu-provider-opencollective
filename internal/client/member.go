// Copyright IBM Corp. 2021, 2026
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"errors"
	"fmt"
)

// ManagedMemberRoles are the team roles that can be granted by invitation.
// Other roles (BACKER, HOST, ...) are created by the platform itself.
var ManagedMemberRoles = []string{"ADMIN", "MEMBER", "ACCOUNTANT"}

// Membership statuses.
const (
	MembershipActive  = "ACTIVE"
	MembershipInvited = "INVITED"
)

// Membership is either an accepted membership or a pending invitation of an
// account to a collective.
type Membership struct {
	// ID is the public identifier of the Member or MemberInvitation.
	ID            string
	Status        string
	Role          string
	Description   *string
	Since         *string
	MemberAccount AccountReference
}

// MemberInput describes a membership to create or edit.
type MemberInput struct {
	CollectiveID string
	// Member identifies the member account by ID or slug.
	Member      AccountReference
	Role        string
	Description *string
	Since       *string
}

func (r AccountReference) input() map[string]any {
	if r.ID != "" {
		return map[string]any{"id": r.ID}
	}

	return map[string]any{"slug": r.Slug}
}

func (in MemberInput) variables() map[string]any {
	v := map[string]any{
		"account":       map[string]any{"id": in.CollectiveID},
		"memberAccount": in.Member.input(),
		"role":          in.Role,
	}
	if in.Description != nil {
		v["description"] = *in.Description
	}
	if in.Since != nil {
		v["since"] = *in.Since
	}

	return v
}

type memberNode struct {
	ID          string            `json:"id"`
	Role        string            `json:"role"`
	Description *string           `json:"description"`
	Since       *string           `json:"since"`
	Account     *AccountReference `json:"account"`
}

type invitationNode struct {
	ID            string            `json:"id"`
	Role          string            `json:"role"`
	Description   *string           `json:"description"`
	Since         *string           `json:"since"`
	MemberAccount *AccountReference `json:"memberAccount"`
}

func (n invitationNode) membership() Membership {
	m := Membership{ID: n.ID, Status: MembershipInvited, Role: n.Role, Description: n.Description, Since: n.Since}
	if n.MemberAccount != nil {
		m.MemberAccount = *n.MemberAccount
	}

	return m
}

const inviteMemberMutation = `mutation InviteMember($account: AccountReferenceInput!, $memberAccount: AccountReferenceInput!, $role: MemberRole!, $description: String, $since: DateTime) {
  inviteMember(account: $account, memberAccount: $memberAccount, role: $role, description: $description, since: $since) {
    id
    role
    description
    since
    memberAccount {
      id
      slug
    }
  }
}`

// InviteMember invites an account to join a collective. The membership stays
// an invitation until the invitee accepts it.
func (c *Client) InviteMember(ctx context.Context, in MemberInput) (*Membership, error) {
	var out struct {
		InviteMember *invitationNode `json:"inviteMember"`
	}

	err := c.DoRequest(ctx, Request{Query: inviteMemberMutation, Variables: in.variables(), OperationName: "InviteMember"}, &out)
	if err != nil {
		return nil, err
	}

	if out.InviteMember == nil {
		return nil, errors.New("inviteMember returned no invitation")
	}

	m := out.InviteMember.membership()

	return &m, nil
}

// membersPageSize is the number of members fetched per request.
const membersPageSize = 100

const getMembershipsQuery = `query GetMemberships($id: String!, $offset: Int!, $limit: Int!, $roles: [MemberRole], $memberAccount: AccountReferenceInput, $withInvitations: Boolean!) {
  collective(id: $id, throwIfMissing: false) {
    members(limit: $limit, offset: $offset, role: $roles, includeInherited: false, orderBy: {field: CREATED_AT, direction: ASC}) {
      totalCount
      nodes {
        id
        role
        description
        since
        account {
          id
          slug
        }
      }
    }
    memberInvitations(memberAccount: $memberAccount, role: $roles) @include(if: $withInvitations) {
      id
      role
      description
      since
      memberAccount {
        id
        slug
      }
    }
  }
}`

// GetMemberships returns the team memberships (see ManagedMemberRoles) and
// pending invitations that member holds in the collective. Pending invitations
// are only visible to the collective's admins. It returns ErrNotFound if the
// collective does not exist.
func (c *Client) GetMemberships(ctx context.Context, collectiveID string, member AccountReference) ([]Membership, error) {
	matches := func(a *AccountReference) bool {
		if a == nil {
			return false
		}
		if member.ID != "" {
			return a.ID == member.ID
		}
		return a.Slug == member.Slug
	}

	var result []Membership

	for offset := 0; ; offset += membersPageSize {
		var out struct {
			Collective *struct {
				Members struct {
					TotalCount int          `json:"totalCount"`
					Nodes      []memberNode `json:"nodes"`
				} `json:"members"`
				MemberInvitations []invitationNode `json:"memberInvitations"`
			} `json:"collective"`
		}

		err := c.DoRequest(ctx, Request{
			Query: getMembershipsQuery,
			Variables: map[string]any{
				"id":              collectiveID,
				"offset":          offset,
				"limit":           membersPageSize,
				"roles":           ManagedMemberRoles,
				"memberAccount":   member.input(),
				"withInvitations": offset == 0,
			},
			OperationName: "GetMemberships",
		}, &out)
		if err != nil {
			return nil, err
		}

		if out.Collective == nil {
			return nil, ErrNotFound
		}

		for _, inv := range out.Collective.MemberInvitations {
			if matches(inv.MemberAccount) {
				result = append(result, inv.membership())
			}
		}

		for _, n := range out.Collective.Members.Nodes {
			if matches(n.Account) {
				result = append(result, Membership{
					ID: n.ID, Status: MembershipActive, Role: n.Role,
					Description: n.Description, Since: n.Since, MemberAccount: *n.Account,
				})
			}
		}

		if len(out.Collective.Members.Nodes) < membersPageSize || offset+membersPageSize >= out.Collective.Members.TotalCount {
			return result, nil
		}
	}
}

const editMemberMutation = `mutation EditMember($account: AccountReferenceInput!, $memberAccount: AccountReferenceInput!, $role: MemberRole, $description: String, $since: DateTime) {
  editMember(account: $account, memberAccount: $memberAccount, role: $role, description: $description, since: $since) {
    id
  }
}`

const editMemberInvitationMutation = `mutation EditMemberInvitation($account: AccountReferenceInput!, $memberAccount: AccountReferenceInput!, $role: MemberRole, $description: String, $since: DateTime) {
  editMemberInvitation(account: $account, memberAccount: $memberAccount, role: $role, description: $description, since: $since) {
    id
  }
}`

// EditMembership updates an accepted membership or a pending invitation,
// depending on status.
func (c *Client) EditMembership(ctx context.Context, status string, in MemberInput) error {
	switch status {
	case MembershipActive:
		return c.DoRequest(ctx, Request{Query: editMemberMutation, Variables: in.variables(), OperationName: "EditMember"}, nil)
	case MembershipInvited:
		return c.DoRequest(ctx, Request{Query: editMemberInvitationMutation, Variables: in.variables(), OperationName: "EditMemberInvitation"}, nil)
	default:
		return fmt.Errorf("unknown membership status %q", status)
	}
}

const removeMemberMutation = `mutation RemoveMember($account: AccountReferenceInput!, $memberAccount: AccountReferenceInput!, $role: MemberRole!, $isInvitation: Boolean) {
  removeMember(account: $account, memberAccount: $memberAccount, role: $role, isInvitation: $isInvitation)
}`

// RemoveMembership removes an accepted membership or cancels a pending
// invitation, depending on status.
func (c *Client) RemoveMembership(ctx context.Context, status, collectiveID string, member AccountReference, role string) error {
	return c.DoRequest(ctx, Request{
		Query: removeMemberMutation,
		Variables: map[string]any{
			"account":       map[string]any{"id": collectiveID},
			"memberAccount": member.input(),
			"role":          role,
			"isInvitation":  status == MembershipInvited,
		},
		OperationName: "RemoveMember",
	}, nil)
}
