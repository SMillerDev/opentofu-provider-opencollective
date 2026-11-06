// Copyright IBM Corp. 2021, 2026
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"errors"
	"fmt"
)

// CollectiveLookup identifies a collective. Exactly one field should be set.
type CollectiveLookup struct {
	ID           string
	Slug         string
	GithubHandle string
}

// SocialLink is a link to a collective's profile on another platform.
type SocialLink struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

// AccountReference is a minimal reference to another account.
type AccountReference struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
}

// Collective is an Open Collective collective account.
type Collective struct {
	ID              string            `json:"id"`
	LegacyID        int64             `json:"legacyId"`
	Slug            string            `json:"slug"`
	Type            string            `json:"type"`
	Name            *string           `json:"name"`
	Description     *string           `json:"description"`
	LongDescription *string           `json:"longDescription"`
	Website         *string           `json:"website"`
	ImageURL        *string           `json:"imageUrl"`
	Currency        string            `json:"currency"`
	Tags            []string          `json:"tags"`
	SocialLinks     []SocialLink      `json:"socialLinks"`
	IsActive        bool              `json:"isActive"`
	IsApproved      bool              `json:"isApproved"`
	IsArchived      bool              `json:"isArchived"`
	CreatedAt       *string           `json:"createdAt"`
	Host            *AccountReference `json:"host"`
	HostApplication *HostApplication  `json:"hostApplication"`
}

// HostApplication is a collective's application to a fiscal host.
type HostApplication struct {
	// Status is one of PENDING, APPROVED, REJECTED or EXPIRED.
	Status string            `json:"status"`
	Host   *AccountReference `json:"host"`
}

// EffectiveHostSlug returns the slug of the collective's host, falling back to
// the host of a pending application, or "" if there is neither.
func (c *Collective) EffectiveHostSlug() string {
	if c.Host != nil {
		return c.Host.Slug
	}

	if c.HostApplication != nil && c.HostApplication.Status == "PENDING" && c.HostApplication.Host != nil {
		return c.HostApplication.Host.Slug
	}

	return ""
}

// collectiveFragment selects every field decoded into Collective.
const collectiveFragment = `fragment CollectiveFields on Collective {
  id
  legacyId
  slug
  type
  name
  description
  longDescription
  website
  imageUrl
  currency
  tags
  socialLinks {
    type
    url
  }
  isActive
  isApproved
  isArchived
  createdAt
  host {
    id
    slug
  }
  hostApplication {
    status
    host {
      id
      slug
    }
  }
}`

const getCollectiveQuery = `query GetCollective($id: String, $slug: String, $githubHandle: String) {
  collective(id: $id, slug: $slug, githubHandle: $githubHandle, throwIfMissing: false) {
    ...CollectiveFields
  }
}
` + collectiveFragment

// ErrNotFound is returned when the requested object does not exist.
var ErrNotFound = errors.New("not found")

// GetCollective fetches a single collective by ID, slug or GitHub handle.
// It returns ErrNotFound if no matching collective exists.
func (c *Client) GetCollective(ctx context.Context, lookup CollectiveLookup) (*Collective, error) {
	variables := map[string]any{}
	if lookup.ID != "" {
		variables["id"] = lookup.ID
	}
	if lookup.Slug != "" {
		variables["slug"] = lookup.Slug
	}
	if lookup.GithubHandle != "" {
		variables["githubHandle"] = lookup.GithubHandle
	}

	if len(variables) == 0 {
		return nil, errors.New("one of ID, Slug or GithubHandle must be set")
	}

	var out struct {
		Collective *Collective `json:"collective"`
	}

	err := c.DoRequest(ctx, Request{
		Query:         getCollectiveQuery,
		Variables:     variables,
		OperationName: "GetCollective",
	}, &out)
	if err != nil {
		return nil, err
	}

	if out.Collective == nil {
		return nil, ErrNotFound
	}

	return out.Collective, nil
}

// CollectiveCreateInput describes a collective to create.
type CollectiveCreateInput struct {
	Name        string   `json:"name"`
	Slug        string   `json:"slug"`
	Description string   `json:"description"`
	Tags        []string `json:"tags,omitempty"`
}

const createCollectiveMutation = `mutation CreateCollective($collective: CollectiveCreateInput!, $host: AccountReferenceInput) {
  createCollective(collective: $collective, host: $host) {
    ...CollectiveFields
  }
}
` + collectiveFragment

// CreateCollective creates a collective, optionally applying to the host
// identified by hostSlug.
func (c *Client) CreateCollective(ctx context.Context, input CollectiveCreateInput, hostSlug string) (*Collective, error) {
	variables := map[string]any{"collective": input}
	if hostSlug != "" {
		variables["host"] = map[string]any{"slug": hostSlug}
	}

	var out struct {
		CreateCollective *Collective `json:"createCollective"`
	}

	if err := c.DoRequest(ctx, Request{Query: createCollectiveMutation, Variables: variables, OperationName: "CreateCollective"}, &out); err != nil {
		return nil, err
	}

	if out.CreateCollective == nil {
		return nil, errors.New("createCollective returned no collective")
	}

	return out.CreateCollective, nil
}

// CollectiveUpdateInput describes the editable properties of a collective.
// All fields are sent, so zero values clear the corresponding property;
// Currency is only sent when non-empty.
type CollectiveUpdateInput struct {
	ID              string
	Name            string
	Slug            string
	Description     string
	LongDescription string
	Tags            []string
	SocialLinks     []SocialLink
	Currency        string
}

const editCollectiveMutation = `mutation EditCollective($account: AccountUpdateInput!) {
  editAccount(account: $account) {
    ...CollectiveFields
  }
}
` + collectiveFragment

// UpdateCollective edits a collective's properties.
func (c *Client) UpdateCollective(ctx context.Context, input CollectiveUpdateInput) (*Collective, error) {
	tags := input.Tags
	if tags == nil {
		tags = []string{}
	}

	links := input.SocialLinks
	if links == nil {
		links = []SocialLink{}
	}

	account := map[string]any{
		"id":              input.ID,
		"name":            input.Name,
		"slug":            input.Slug,
		"description":     input.Description,
		"longDescription": input.LongDescription,
		"tags":            tags,
		"socialLinks":     links,
	}
	if input.Currency != "" {
		account["currency"] = input.Currency
	}

	var out struct {
		EditAccount *Collective `json:"editAccount"`
	}

	err := c.DoRequest(ctx, Request{
		Query:         editCollectiveMutation,
		Variables:     map[string]any{"account": account},
		OperationName: "EditCollective",
	}, &out)
	if err != nil {
		return nil, err
	}

	if out.EditAccount == nil {
		return nil, errors.New("editAccount returned no collective")
	}

	return out.EditAccount, nil
}

const applyToHostMutation = `mutation ApplyToHost($collective: AccountReferenceInput!, $host: AccountReferenceInput!) {
  applyToHost(collective: $collective, host: $host) {
    id
  }
}`

// ApplyToHost applies the collective with the given ID to the host with the given slug.
func (c *Client) ApplyToHost(ctx context.Context, collectiveID, hostSlug string) error {
	return c.DoRequest(ctx, Request{
		Query: applyToHostMutation,
		Variables: map[string]any{
			"collective": map[string]any{"id": collectiveID},
			"host":       map[string]any{"slug": hostSlug},
		},
		OperationName: "ApplyToHost",
	}, nil)
}

const removeHostMutation = `mutation RemoveHost($account: AccountReferenceInput!) {
  removeHost(account: $account, pauseContributions: true) {
    id
  }
}`

// RemoveHost unhosts the collective with the given ID. Recurring
// contributions are paused rather than cancelled.
func (c *Client) RemoveHost(ctx context.Context, collectiveID string) error {
	return c.DoRequest(ctx, Request{
		Query:         removeHostMutation,
		Variables:     map[string]any{"account": map[string]any{"id": collectiveID}},
		OperationName: "RemoveHost",
	}, nil)
}

const deleteAccountMutation = `mutation DeleteAccount($account: AccountReferenceInput!) {
  deleteAccount(account: $account) {
    id
  }
}`

// DeleteAccount permanently deletes the account with the given ID. The API
// refuses to delete accounts with financial activity; use ArchiveAccount for those.
func (c *Client) DeleteAccount(ctx context.Context, id string) error {
	return c.accountMutation(ctx, deleteAccountMutation, "DeleteAccount", id)
}

const archiveAccountMutation = `mutation ArchiveAccount($account: AccountReferenceInput!) {
  archiveAccount(account: $account) {
    id
  }
}`

// ArchiveAccount archives the account with the given ID.
func (c *Client) ArchiveAccount(ctx context.Context, id string) error {
	return c.accountMutation(ctx, archiveAccountMutation, "ArchiveAccount", id)
}

func (c *Client) accountMutation(ctx context.Context, query, op, id string) error {
	if id == "" {
		return fmt.Errorf("%s: account ID must be set", op)
	}

	return c.DoRequest(ctx, Request{
		Query:         query,
		Variables:     map[string]any{"account": map[string]any{"id": id}},
		OperationName: op,
	}, nil)
}
