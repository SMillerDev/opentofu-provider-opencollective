// Copyright IBM Corp. 2021, 2026
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/SMillerDev/opentofu-provider-opencollective/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var (
	_ resource.Resource                = &CollectiveMemberResource{}
	_ resource.ResourceWithConfigure   = &CollectiveMemberResource{}
	_ resource.ResourceWithImportState = &CollectiveMemberResource{}
)

func NewCollectiveMemberResource() resource.Resource {
	return &CollectiveMemberResource{}
}

// CollectiveMemberResource manages a team membership (admin, core
// contributor or accountant) of a collective.
type CollectiveMemberResource struct {
	client *client.Client
}

// CollectiveMemberResourceModel describes the resource data model.
type CollectiveMemberResourceModel struct {
	ID              types.String `tfsdk:"id"`
	CollectiveID    types.String `tfsdk:"collective_id"`
	MemberSlug      types.String `tfsdk:"member_slug"`
	MemberAccountID types.String `tfsdk:"member_account_id"`
	Role            types.String `tfsdk:"role"`
	Description     types.String `tfsdk:"description"`
	Since           types.String `tfsdk:"since"`
	Status          types.String `tfsdk:"status"`
}

func (r *CollectiveMemberResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_collective_member"
}

func (r *CollectiveMemberResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a team member (admin, core contributor or accountant) of an Open Collective collective. " +
			"Requires a `personal_token` with the `account` scope, belonging to an admin of the collective.\n\n" +
			"Creating this resource sends an invitation; `status` is `INVITED` until the invitee accepts it and " +
			"`ACTIVE` afterwards. If the invitation is declined, or the member is removed outside OpenTofu, the " +
			"resource disappears from state and the next apply sends a new invitation.",

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Identifier of the membership, `<collective_id>/<member_account_id>`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"collective_id": schema.StringAttribute{
				MarkdownDescription: "Public ID of the collective, e.g. `opencollective_collective.example.id`.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"member_slug": schema.StringAttribute{
				MarkdownDescription: "Slug of the account to add, e.g. `jane-doe` for `https://opencollective.com/jane-doe`.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"member_account_id": schema.StringAttribute{
				MarkdownDescription: "Public ID of the member's account. Used to track the member if their slug changes.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"role": schema.StringAttribute{
				MarkdownDescription: "Role of the member: `ADMIN`, `MEMBER` (core contributor) or `ACCOUNTANT`.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.OneOf(client.ManagedMemberRoles...)},
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "Description of the member's role, shown on the collective's page.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"since": schema.StringAttribute{
				MarkdownDescription: "Date the membership started (RFC 3339). Defaults to when the invitation is accepted.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				Validators:          []validator.String{rfc3339Validator{}},
			},
			"status": schema.StringAttribute{
				MarkdownDescription: "`INVITED` while the invitation is pending, `ACTIVE` once accepted.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *CollectiveMemberResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *client.Client, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)

		return
	}

	r.client = c
}

func (r *CollectiveMemberResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan CollectiveMemberResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	m, err := r.client.InviteMember(ctx, plan.input(client.AccountReference{Slug: plan.MemberSlug.ValueString()}))
	if err != nil {
		resp.Diagnostics.AddError("Unable to invite member", err.Error())
		return
	}

	tflog.Trace(ctx, "invited collective member", map[string]any{"collective_id": plan.CollectiveID.ValueString(), "member": m.MemberAccount.Slug})

	plan.fromAPI(m)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *CollectiveMemberResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state CollectiveMemberResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	m, err := r.find(ctx, &state)
	if errors.Is(err, client.ErrNotFound) {
		tflog.Warn(ctx, "membership no longer exists, removing from state", map[string]any{"id": state.ID.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read collective member", err.Error())
		return
	}

	state.fromAPI(m)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *CollectiveMemberResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state CollectiveMemberResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Look the membership up again: the invitation may have been accepted
	// since the last refresh, which changes the mutation to use.
	current, err := r.find(ctx, &state)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read collective member", err.Error())
		return
	}

	member := client.AccountReference{ID: state.MemberAccountID.ValueString()}

	in := plan.input(member)
	if in.Description == nil {
		empty := ""
		in.Description = &empty // clear a previously set description
	}

	if err := r.client.EditMembership(ctx, current.Status, in); err != nil {
		resp.Diagnostics.AddError("Unable to update collective member", err.Error())
		return
	}

	plan.ID, plan.MemberAccountID = state.ID, state.MemberAccountID

	updated, err := r.find(ctx, &plan)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read collective member after update", err.Error())
		return
	}

	plan.fromAPI(updated)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *CollectiveMemberResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state CollectiveMemberResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	current, err := r.find(ctx, &state)
	if errors.Is(err, client.ErrNotFound) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read collective member", err.Error())
		return
	}

	err = r.client.RemoveMembership(ctx, current.Status, state.CollectiveID.ValueString(), current.MemberAccount, current.Role)
	if err != nil {
		resp.Diagnostics.AddError("Unable to remove collective member", err.Error())
	}
}

// ImportState accepts `<collective id or slug>/<member slug>`.
func (r *CollectiveMemberResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	collectiveRef, memberSlug, ok := strings.Cut(req.ID, "/")
	if !ok || collectiveRef == "" || memberSlug == "" {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("Expected <collective id or slug>/<member slug>, got %q.", req.ID))
		return
	}

	collective, err := r.client.GetCollective(ctx, client.CollectiveLookup{ID: collectiveRef})
	if errors.Is(err, client.ErrNotFound) {
		collective, err = r.client.GetCollective(ctx, client.CollectiveLookup{Slug: collectiveRef})
	}
	if errors.Is(err, client.ErrNotFound) {
		resp.Diagnostics.AddError("Collective not found", fmt.Sprintf("No collective has ID or slug %q.", collectiveRef))
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to import collective member", err.Error())
		return
	}

	state := CollectiveMemberResourceModel{
		CollectiveID: types.StringValue(collective.ID),
		MemberSlug:   types.StringValue(memberSlug),
	}

	m, err := r.find(ctx, &state)
	if errors.Is(err, client.ErrNotFound) {
		resp.Diagnostics.AddError("Member not found", fmt.Sprintf("%q is not a member of, nor invited to, collective %q.", memberSlug, collectiveRef))
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to import collective member", err.Error())
		return
	}

	state.fromAPI(m)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// find returns the membership tracked by m. It returns client.ErrNotFound if
// the collective or the membership no longer exists.
func (r *CollectiveMemberResource) find(ctx context.Context, m *CollectiveMemberResourceModel) (*client.Membership, error) {
	member := client.AccountReference{ID: m.MemberAccountID.ValueString()}
	if member.ID == "" {
		member.Slug = m.MemberSlug.ValueString()
	}

	memberships, err := r.client.GetMemberships(ctx, m.CollectiveID.ValueString(), member)
	if err != nil {
		return nil, err
	}

	if best := pickMembership(memberships, m.Role.ValueString()); best != nil {
		return best, nil
	}

	return nil, client.ErrNotFound
}

// pickMembership chooses the membership matching role, preferring accepted
// memberships over invitations. If none has the role (it was changed outside
// OpenTofu) any membership is returned, so the role change shows up as a diff.
func pickMembership(ms []client.Membership, role string) *client.Membership {
	rank := func(m client.Membership) int {
		r := 0
		if m.Role == role {
			r += 2
		}
		if m.Status == client.MembershipActive {
			r++
		}
		return r
	}

	var best *client.Membership
	for i := range ms {
		if best == nil || rank(ms[i]) > rank(*best) {
			best = &ms[i]
		}
	}

	return best
}

func (m *CollectiveMemberResourceModel) input(member client.AccountReference) client.MemberInput {
	in := client.MemberInput{
		CollectiveID: m.CollectiveID.ValueString(),
		Member:       member,
		Role:         m.Role.ValueString(),
	}

	if !m.Description.IsNull() && !m.Description.IsUnknown() {
		in.Description = m.Description.ValueStringPointer()
	}

	if !m.Since.IsNull() && !m.Since.IsUnknown() {
		in.Since = m.Since.ValueStringPointer()
	}

	return in
}

// fromAPI copies the membership into the model. member_slug is kept as
// configured so a renamed member account does not force a replacement, and a
// configured since is kept when the API returns the same instant in a
// different format.
func (m *CollectiveMemberResourceModel) fromAPI(ms *client.Membership) {
	m.MemberAccountID = types.StringValue(ms.MemberAccount.ID)
	m.ID = types.StringValue(m.CollectiveID.ValueString() + "/" + ms.MemberAccount.ID)
	m.Role = types.StringValue(ms.Role)
	m.Status = types.StringValue(ms.Status)

	if m.MemberSlug.IsNull() || m.MemberSlug.IsUnknown() {
		m.MemberSlug = types.StringValue(ms.MemberAccount.Slug)
	}

	if ms.Description != nil && *ms.Description != "" {
		m.Description = types.StringValue(*ms.Description)
	} else {
		m.Description = types.StringNull()
	}

	known := !m.Since.IsNull() && !m.Since.IsUnknown()

	switch {
	case ms.Since == nil && known:
		// The API did not report a start date (e.g. for a pending
		// invitation); keep the requested one.
	case ms.Since == nil:
		m.Since = types.StringNull()
	case known && sameInstant(m.Since.ValueString(), *ms.Since):
		// Keep the configured representation of the same instant.
	default:
		m.Since = types.StringValue(*ms.Since)
	}
}
