// Copyright IBM Corp. 2021, 2026
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/SMillerDev/opentofu-provider-opencollective/internal/client"
)

// fakeAPI is a minimal, stateful stand-in for the Open Collective GraphQL API.
// It dispatches on operationName rather than parsing queries, so it only
// supports the operations issued by internal/client.
//
// Host applications stay PENDING (host unset) until approveHost is called,
// mirroring the real API before a host accepts an application.
type fakeAPI struct {
	t     *testing.T
	srv   *httptest.Server
	mu    sync.Mutex
	next  int
	byID  map[string]*client.Collective
	calls []string
	// members holds memberships and invitations, keyed by collective ID.
	members map[string][]*fakeMember
}

type fakeMember struct {
	id          string
	status      string
	role        string
	description *string
	since       *string
	account     client.AccountReference
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()

	f := &fakeAPI{t: t, byID: map[string]*client.Collective{}, members: map[string][]*fakeMember{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)

	return f
}

// providerConfig returns a provider block pointing at the fake API.
func (f *fakeAPI) providerConfig() string {
	return fmt.Sprintf(`
provider "opencollective" {
  endpoint       = %q
  personal_token = "test-token"
}
`, f.srv.URL)
}

func (f *fakeAPI) called(op string) int {
	f.mu.Lock()
	defer f.mu.Unlock()

	n := 0
	for _, c := range f.calls {
		if c == op {
			n++
		}
	}

	return n
}

func (f *fakeAPI) bySlug(slug string) *client.Collective {
	for _, c := range f.byID {
		if c.Slug == slug {
			return c
		}
	}

	return nil
}

func (f *fakeAPI) approveHost(slug string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	c := f.bySlug(slug)
	c.Host = c.HostApplication.Host
	c.HostApplication.Status = "APPROVED"
	c.IsApproved, c.IsActive = true, true
}

func (f *fakeAPI) deleteOutOfBand(slug string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	delete(f.byID, f.bySlug(slug).ID)
}

// findMember returns the index of the membership of account (matched by ID
// or slug) in the collective, filtered by status and, if non-empty, role.
func (f *fakeAPI) findMember(collectiveID string, account map[string]string, status, role string) int {
	for i, m := range f.members[collectiveID] {
		if m.status != status || (role != "" && m.role != role) {
			continue
		}
		if m.matches(account) {
			return i
		}
	}

	return -1
}

func (f *fakeAPI) memberBySlug(memberSlug string) *fakeMember {
	for _, ms := range f.members {
		for _, m := range ms {
			if m.account.Slug == memberSlug {
				return m
			}
		}
	}

	return nil
}

// acceptInvitation simulates the invitee accepting their invitation.
func (f *fakeAPI) acceptInvitation(memberSlug string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	m := f.memberBySlug(memberSlug)
	m.status = client.MembershipActive
	if m.since == nil {
		accepted := "2026-02-01T00:00:00.000Z"
		m.since = &accepted
	}
}

// removeMemberOutOfBand simulates a declined invitation or a member removed in the UI.
func (f *fakeAPI) removeMemberOutOfBand(memberSlug string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for cid, ms := range f.members {
		for i, m := range ms {
			if m.account.Slug == memberSlug {
				f.members[cid] = append(ms[:i], ms[i+1:]...)
				return
			}
		}
	}
}

// setRoleOutOfBand simulates a member's role being changed in the UI.
func (f *fakeAPI) setRoleOutOfBand(memberSlug, role string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.memberBySlug(memberSlug).role = role
}

// renameAccount simulates a member account changing its slug.
func (f *fakeAPI) renameAccount(oldSlug, newSlug string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.memberBySlug(oldSlug).account.Slug = newSlug
}

// addActiveMembers adds n unrelated active members to a collective so the
// membership lookup has to page through results.
func (f *fakeAPI) addActiveMembers(collectiveSlug string, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()

	c := f.bySlug(collectiveSlug)
	for i := range n {
		f.next++
		f.members[c.ID] = append(f.members[c.ID], &fakeMember{
			id: fmt.Sprintf("member-filler-%d", f.next), status: client.MembershipActive, role: "MEMBER",
			account: client.AccountReference{ID: fmt.Sprintf("acct-filler-%d", i), Slug: fmt.Sprintf("filler-%d", i)},
		})
	}
}

// normalizeSince mimics the API, which returns timestamps with milliseconds.
func normalizeSince(s *string) *string {
	if s == nil {
		return nil
	}

	t, err := time.Parse(time.RFC3339Nano, *s)
	if err != nil {
		return s
	}

	out := t.UTC().Format("2006-01-02T15:04:05.000Z")

	return &out
}

func (m *fakeMember) matches(account map[string]string) bool {
	return (account["id"] != "" && m.account.ID == account["id"]) || (account["slug"] != "" && m.account.Slug == account["slug"])
}

func (m *fakeMember) node(accountField string) map[string]any {
	return map[string]any{
		"id": m.id, "role": m.role, "description": m.description, "since": m.since,
		accountField: map[string]any{"id": m.account.ID, "slug": m.account.Slug},
	}
}

func (f *fakeAPI) handle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OperationName string          `json:"operationName"`
		Variables     json.RawMessage `json:"variables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		f.t.Errorf("fake API: decoding request: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, req.OperationName)

	isMutation := req.OperationName != "GetCollective" && req.OperationName != "GetMemberships"
	if isMutation && r.Header.Get("Personal-Token") != "test-token" {
		writeGraphQL(w, nil, "You need to be logged in")
		return
	}

	var vars struct {
		ID         string                     `json:"id"`
		Slug       string                     `json:"slug"`
		Collective json.RawMessage            `json:"collective"`
		Host       *struct{ Slug string }     `json:"host"`
		Account    map[string]json.RawMessage `json:"account"`

		MemberAccount   map[string]string `json:"memberAccount"`
		Role            string            `json:"role"`
		Description     *string           `json:"description"`
		Since           *string           `json:"since"`
		Offset          int               `json:"offset"`
		Limit           int               `json:"limit"`
		WithInvitations bool              `json:"withInvitations"`
		IsInvitation    bool              `json:"isInvitation"`
	}
	if len(req.Variables) > 0 {
		if err := json.Unmarshal(req.Variables, &vars); err != nil {
			f.t.Errorf("fake API: decoding variables: %v", err)
		}
	}

	accountID := func() string {
		var id string
		_ = json.Unmarshal(vars.Account["id"], &id)
		return id
	}

	switch req.OperationName {
	case "GetCollective":
		c := f.byID[vars.ID]
		if vars.Slug != "" {
			c = f.bySlug(vars.Slug)
		}
		writeGraphQL(w, map[string]any{"collective": c}, "")

	case "CreateCollective":
		var in client.CollectiveCreateInput
		_ = json.Unmarshal(vars.Collective, &in)
		if f.bySlug(in.Slug) != nil {
			writeGraphQL(w, nil, "The slug "+in.Slug+" is already taken")
			return
		}

		f.next++
		created := time.Date(2026, 1, 1, 0, 0, f.next, 0, time.UTC).Format(time.RFC3339)
		c := &client.Collective{
			ID: fmt.Sprintf("fake-%04d", f.next), LegacyID: int64(1000 + f.next),
			Slug: in.Slug, Type: "COLLECTIVE", Name: &in.Name, Description: &in.Description,
			Currency: "USD", Tags: in.Tags, CreatedAt: &created,
		}
		if vars.Host != nil {
			c.HostApplication = &client.HostApplication{Status: "PENDING", Host: &client.AccountReference{ID: "host-" + vars.Host.Slug, Slug: vars.Host.Slug}}
		}
		f.byID[c.ID] = c
		writeGraphQL(w, map[string]any{"createCollective": c}, "")

	case "EditCollective":
		c := f.byID[accountID()]
		if c == nil {
			writeGraphQL(w, nil, "Account not found")
			return
		}

		var in struct {
			Name, Slug, Description, LongDescription, Currency string
			Tags                                               []string
			SocialLinks                                        []client.SocialLink
		}
		raw, _ := json.Marshal(vars.Account)
		_ = json.Unmarshal(raw, &in)

		c.Name, c.Slug, c.Description = &in.Name, in.Slug, &in.Description
		c.LongDescription = &in.LongDescription
		c.Tags, c.SocialLinks = in.Tags, in.SocialLinks
		if in.Currency != "" {
			c.Currency = in.Currency
		}
		c.Website = nil
		for _, l := range in.SocialLinks {
			if l.Type == "WEBSITE" {
				c.Website = &l.URL
			}
		}
		writeGraphQL(w, map[string]any{"editAccount": c}, "")

	case "ApplyToHost":
		var ref struct{ ID string }
		_ = json.Unmarshal(vars.Collective, &ref)
		c := f.byID[ref.ID]
		c.Host, c.IsApproved, c.IsActive = nil, false, false
		c.HostApplication = &client.HostApplication{Status: "PENDING", Host: &client.AccountReference{ID: "host-" + vars.Host.Slug, Slug: vars.Host.Slug}}
		writeGraphQL(w, map[string]any{"applyToHost": map[string]any{"id": c.ID}}, "")

	case "RemoveHost":
		c := f.byID[accountID()]
		c.Host, c.HostApplication, c.IsApproved, c.IsActive = nil, nil, false, false
		writeGraphQL(w, map[string]any{"removeHost": map[string]any{"id": c.ID}}, "")

	case "DeleteAccount":
		delete(f.byID, accountID())
		writeGraphQL(w, map[string]any{"deleteAccount": map[string]any{"id": accountID()}}, "")

	case "ArchiveAccount":
		if c := f.byID[accountID()]; c != nil {
			c.IsArchived = true
		}
		writeGraphQL(w, map[string]any{"archiveAccount": map[string]any{"id": accountID()}}, "")

	case "GetMemberships":
		if f.byID[vars.ID] == nil {
			writeGraphQL(w, map[string]any{"collective": nil}, "")
			return
		}

		var active []map[string]any
		var invitations []map[string]any
		for _, m := range f.members[vars.ID] {
			if m.status == client.MembershipActive {
				active = append(active, m.node("account"))
			} else if m.matches(vars.MemberAccount) {
				invitations = append(invitations, m.node("memberAccount"))
			}
		}

		page := active[min(vars.Offset, len(active)):min(vars.Offset+vars.Limit, len(active))]
		collective := map[string]any{"members": map[string]any{"totalCount": len(active), "nodes": page}}
		if vars.WithInvitations {
			// Like the real API, pending invitations are only visible to admins.
			if r.Header.Get("Personal-Token") == "test-token" {
				collective["memberInvitations"] = invitations
			} else {
				collective["memberInvitations"] = nil
			}
		}
		writeGraphQL(w, map[string]any{"collective": collective}, "")

	case "InviteMember":
		cid := accountID()
		if f.byID[cid] == nil {
			writeGraphQL(w, nil, "Collective not found")
			return
		}
		if f.findMember(cid, vars.MemberAccount, client.MembershipActive, vars.Role) >= 0 ||
			f.findMember(cid, vars.MemberAccount, client.MembershipInvited, vars.Role) >= 0 {
			writeGraphQL(w, nil, "This member already exists or has been invited with this role")
			return
		}

		f.next++
		m := &fakeMember{
			id: fmt.Sprintf("invitation-%d", f.next), status: client.MembershipInvited, role: vars.Role,
			description: vars.Description, since: normalizeSince(vars.Since),
			account: client.AccountReference{ID: "acct-" + vars.MemberAccount["slug"], Slug: vars.MemberAccount["slug"]},
		}
		f.members[cid] = append(f.members[cid], m)
		writeGraphQL(w, map[string]any{"inviteMember": m.node("memberAccount")}, "")

	case "EditMember", "EditMemberInvitation":
		status := client.MembershipActive
		if req.OperationName == "EditMemberInvitation" {
			status = client.MembershipInvited
		}

		i := f.findMember(accountID(), vars.MemberAccount, status, "")
		if i < 0 {
			writeGraphQL(w, nil, "Member not found")
			return
		}

		m := f.members[accountID()][i]
		if vars.Role != "" {
			m.role = vars.Role
		}
		if vars.Description != nil {
			m.description = vars.Description
		}
		if vars.Since != nil {
			m.since = normalizeSince(vars.Since)
		}
		writeGraphQL(w, map[string]any{"editMember": map[string]any{"id": m.id}}, "")

	case "RemoveMember":
		status := client.MembershipActive
		if vars.IsInvitation {
			status = client.MembershipInvited
		}

		cid := accountID()
		i := f.findMember(cid, vars.MemberAccount, status, vars.Role)
		if i < 0 {
			writeGraphQL(w, nil, "Member not found")
			return
		}

		f.members[cid] = append(f.members[cid][:i], f.members[cid][i+1:]...)
		writeGraphQL(w, map[string]any{"removeMember": true}, "")

	default:
		f.t.Errorf("fake API: unsupported operation %q", req.OperationName)
		writeGraphQL(w, nil, "unsupported operation")
	}
}

func writeGraphQL(w http.ResponseWriter, data any, errMsg string) {
	resp := map[string]any{"data": data}
	if errMsg != "" {
		resp["errors"] = []map[string]any{{"message": errMsg}}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
