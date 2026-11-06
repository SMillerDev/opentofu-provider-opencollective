// Copyright IBM Corp. 2021, 2026
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/SMillerDev/opentofu-provider-opencollective/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var (
	_ resource.Resource                = &CollectiveResource{}
	_ resource.ResourceWithConfigure   = &CollectiveResource{}
	_ resource.ResourceWithImportState = &CollectiveResource{}
)

// socialLinkTypes are the values accepted by the API's SocialLinkType enum.
var socialLinkTypes = []string{
	"BLUESKY", "DISCORD", "DISCOURSE", "FACEBOOK", "GHOST", "GITHUB", "GITLAB",
	"INSTAGRAM", "LINKEDIN", "MASTODON", "MATTERMOST", "MEETUP", "PEERTUBE",
	"PIXELFED", "SLACK", "THREADS", "TIKTOK", "TUMBLR", "TWITCH", "TWITTER",
	"WEBSITE", "YOUTUBE",
}

func NewCollectiveResource() resource.Resource {
	return &CollectiveResource{}
}

// CollectiveResource manages an Open Collective collective.
type CollectiveResource struct {
	client *client.Client
}

// CollectiveResourceModel describes the resource data model.
type CollectiveResourceModel struct {
	ID               types.String `tfsdk:"id"`
	Slug             types.String `tfsdk:"slug"`
	Name             types.String `tfsdk:"name"`
	Description      types.String `tfsdk:"description"`
	LongDescription  types.String `tfsdk:"long_description"`
	Tags             types.Set    `tfsdk:"tags"`
	SocialLinks      types.List   `tfsdk:"social_links"`
	Currency         types.String `tfsdk:"currency"`
	HostSlug         types.String `tfsdk:"host_slug"`
	HostAppStatus    types.String `tfsdk:"host_application_status"`
	ArchiveOnDestroy types.Bool   `tfsdk:"archive_on_destroy"`
	LegacyID         types.Int64  `tfsdk:"legacy_id"`
	Type             types.String `tfsdk:"type"`
	Website          types.String `tfsdk:"website"`
	ImageURL         types.String `tfsdk:"image_url"`
	IsActive         types.Bool   `tfsdk:"is_active"`
	IsApproved       types.Bool   `tfsdk:"is_approved"`
	IsArchived       types.Bool   `tfsdk:"is_archived"`
	CreatedAt        types.String `tfsdk:"created_at"`
}

func (r *CollectiveResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_collective"
}

func (r *CollectiveResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages an Open Collective collective. Requires a `personal_token` with the `account` scope.\n\n" +
			"Changing `host_slug` removes the current host (pausing, not cancelling, recurring contributions) " +
			"and/or applies to the new host; the new host must still approve the application.",

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Public identifier of the collective.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"slug": schema.StringAttribute{
				MarkdownDescription: "Slug of the collective, used in its URL (`https://opencollective.com/<slug>`).",
				Required:            true,
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Public name of the collective.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "Short description of the collective.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"long_description": schema.StringAttribute{
				MarkdownDescription: "Long description of the collective (HTML).",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"tags": schema.SetAttribute{
				MarkdownDescription: "Tags assigned to the collective.",
				ElementType:         types.StringType,
				Optional:            true,
				Validators: []validator.Set{
					setvalidator.SizeAtLeast(1),
					setvalidator.ValueStringsAre(stringvalidator.LengthAtLeast(1)),
				},
			},
			"social_links": schema.ListNestedAttribute{
				MarkdownDescription: "Links to the collective's profiles on other platforms, in order of preference.",
				Optional:            true,
				Validators:          []validator.List{listvalidator.SizeAtLeast(1)},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"type": schema.StringAttribute{
							MarkdownDescription: "Platform type, e.g. `GITHUB` or `WEBSITE`.",
							Required:            true,
							Validators:          []validator.String{stringvalidator.OneOf(socialLinkTypes...)},
						},
						"url": schema.StringAttribute{
							MarkdownDescription: "URL of the profile.",
							Required:            true,
						},
					},
				},
			},
			"currency": schema.StringAttribute{
				MarkdownDescription: "Currency code of the collective, e.g. `USD`. Defaults to the host's currency when not set.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"host_slug": schema.StringAttribute{
				MarkdownDescription: "Slug of the fiscal host to apply to, e.g. `opensource`. " +
					"A pending application counts as the host; see `host_application_status`.",
				Optional:   true,
				Validators: []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"host_application_status": schema.StringAttribute{
				MarkdownDescription: "Status of the most recent host application: `PENDING`, `APPROVED`, `REJECTED` or `EXPIRED`.",
				Computed:            true,
			},
			"archive_on_destroy": schema.BoolAttribute{
				MarkdownDescription: "Archive the collective on destroy instead of deleting it. " +
					"Collectives with financial activity cannot be deleted, so set this for those. Defaults to `false`.",
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(false),
			},
			"legacy_id": schema.Int64Attribute{
				MarkdownDescription: "Legacy numeric identifier of the collective.",
				Computed:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"type": schema.StringAttribute{
				MarkdownDescription: "Account type, always `COLLECTIVE`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"website": schema.StringAttribute{
				MarkdownDescription: "Website of the collective, derived from its social links.",
				Computed:            true,
			},
			"image_url": schema.StringAttribute{
				MarkdownDescription: "URL of the collective's logo.",
				Computed:            true,
			},
			"is_active": schema.BoolAttribute{
				MarkdownDescription: "Whether the collective is active (has an approved host and can receive contributions).",
				Computed:            true,
			},
			"is_approved": schema.BoolAttribute{
				MarkdownDescription: "Whether the collective has been approved by its host.",
				Computed:            true,
			},
			"is_archived": schema.BoolAttribute{
				MarkdownDescription: "Whether the collective is archived.",
				Computed:            true,
			},
			"created_at": schema.StringAttribute{
				MarkdownDescription: "Creation timestamp (RFC 3339).",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *CollectiveResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *CollectiveResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan CollectiveResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	update, diags := plan.updateInput(ctx)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	collective, err := r.client.CreateCollective(ctx, client.CollectiveCreateInput{
		Name:        update.Name,
		Slug:        update.Slug,
		Description: update.Description,
		Tags:        update.Tags,
	}, plan.HostSlug.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to create collective", err.Error())
		return
	}

	tflog.Trace(ctx, "created collective", map[string]any{"id": collective.ID})

	// Save the ID straight away so a failure below doesn't orphan the collective.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), collective.ID)...)

	// Properties that createCollective does not accept are set with a follow-up edit.
	if !plan.LongDescription.IsNull() || !plan.SocialLinks.IsNull() || !plan.Currency.IsUnknown() {
		update.ID = collective.ID
		collective, err = r.client.UpdateCollective(ctx, update)
		if err != nil {
			resp.Diagnostics.AddError("Unable to configure created collective",
				fmt.Sprintf("Collective %s was created, but setting its remaining properties failed: %s", update.ID, err))
			return
		}
	}

	resp.Diagnostics.Append(plan.fromAPI(ctx, collective)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *CollectiveResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state CollectiveResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	collective, err := r.client.GetCollective(ctx, client.CollectiveLookup{ID: state.ID.ValueString()})
	if errors.Is(err, client.ErrNotFound) {
		tflog.Warn(ctx, "collective no longer exists, removing from state", map[string]any{"id": state.ID.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read collective", err.Error())
		return
	}

	resp.Diagnostics.Append(state.fromAPI(ctx, collective)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *CollectiveResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state CollectiveResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	update, diags := plan.updateInput(ctx)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	update.ID = state.ID.ValueString()

	collective, err := r.client.UpdateCollective(ctx, update)
	if err != nil {
		resp.Diagnostics.AddError("Unable to update collective", err.Error())
		return
	}

	if !plan.HostSlug.Equal(state.HostSlug) {
		if !state.HostSlug.IsNull() {
			if err := r.client.RemoveHost(ctx, update.ID); err != nil {
				resp.Diagnostics.AddError("Unable to remove collective host", err.Error())
				return
			}
		}

		if !plan.HostSlug.IsNull() {
			if err := r.client.ApplyToHost(ctx, update.ID, plan.HostSlug.ValueString()); err != nil {
				resp.Diagnostics.AddError("Unable to apply to host", err.Error())
				return
			}
		}

		// Re-read so host and approval state reflect the change.
		collective, err = r.client.GetCollective(ctx, client.CollectiveLookup{ID: update.ID})
		if err != nil {
			resp.Diagnostics.AddError("Unable to read collective after changing host", err.Error())
			return
		}
	}

	resp.Diagnostics.Append(plan.fromAPI(ctx, collective)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *CollectiveResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state CollectiveResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id := state.ID.ValueString()

	if state.ArchiveOnDestroy.ValueBool() {
		if err := r.client.ArchiveAccount(ctx, id); err != nil {
			resp.Diagnostics.AddError("Unable to archive collective", err.Error())
		}

		return
	}

	if err := r.client.DeleteAccount(ctx, id); err != nil {
		resp.Diagnostics.AddError("Unable to delete collective",
			fmt.Sprintf("%s\n\nCollectives with financial activity cannot be deleted; set archive_on_destroy = true to archive it instead.", err))
	}
}

// ImportState accepts either the collective's public ID or its slug.
func (r *CollectiveResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	collective, err := r.client.GetCollective(ctx, client.CollectiveLookup{ID: req.ID})
	if errors.Is(err, client.ErrNotFound) {
		collective, err = r.client.GetCollective(ctx, client.CollectiveLookup{Slug: req.ID})
	}
	if errors.Is(err, client.ErrNotFound) {
		resp.Diagnostics.AddError("Collective not found", fmt.Sprintf("No collective has ID or slug %q.", req.ID))
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to import collective", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), collective.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("archive_on_destroy"), false)...)
}

// updateInput converts the planned configuration into an API update.
func (m *CollectiveResourceModel) updateInput(ctx context.Context) (client.CollectiveUpdateInput, diag.Diagnostics) {
	var diags diag.Diagnostics

	in := client.CollectiveUpdateInput{
		ID:              m.ID.ValueString(),
		Name:            m.Name.ValueString(),
		Slug:            m.Slug.ValueString(),
		Description:     m.Description.ValueString(),
		LongDescription: m.LongDescription.ValueString(),
	}

	if !m.Currency.IsUnknown() {
		in.Currency = m.Currency.ValueString()
	}

	if !m.Tags.IsNull() && !m.Tags.IsUnknown() {
		diags.Append(m.Tags.ElementsAs(ctx, &in.Tags, false)...)
	}

	if !m.SocialLinks.IsNull() && !m.SocialLinks.IsUnknown() {
		var links []struct {
			Type types.String `tfsdk:"type"`
			URL  types.String `tfsdk:"url"`
		}
		diags.Append(m.SocialLinks.ElementsAs(ctx, &links, false)...)

		for _, l := range links {
			in.SocialLinks = append(in.SocialLinks, client.SocialLink{Type: l.Type.ValueString(), URL: l.URL.ValueString()})
		}
	}

	return in, diags
}

// fromAPI copies the API representation into the model. Empty optional
// values from the API are stored as null; the schema validators reject empty
// values in configuration, so this never conflicts with the plan.
func (m *CollectiveResourceModel) fromAPI(ctx context.Context, c *client.Collective) diag.Diagnostics {
	var diags diag.Diagnostics

	m.ID = types.StringValue(c.ID)
	m.Slug = types.StringValue(c.Slug)
	m.Name = types.StringPointerValue(c.Name)
	m.Description = types.StringPointerValue(c.Description)
	m.Currency = types.StringValue(c.Currency)
	m.LegacyID = types.Int64Value(c.LegacyID)
	m.Type = types.StringValue(c.Type)
	m.Website = types.StringPointerValue(c.Website)
	m.ImageURL = types.StringPointerValue(c.ImageURL)
	m.IsActive = types.BoolValue(c.IsActive)
	m.IsApproved = types.BoolValue(c.IsApproved)
	m.IsArchived = types.BoolValue(c.IsArchived)
	m.CreatedAt = types.StringPointerValue(c.CreatedAt)

	if m.ArchiveOnDestroy.IsNull() || m.ArchiveOnDestroy.IsUnknown() {
		m.ArchiveOnDestroy = types.BoolValue(false)
	}

	if c.LongDescription != nil && *c.LongDescription != "" {
		m.LongDescription = types.StringValue(*c.LongDescription)
	} else {
		m.LongDescription = types.StringNull()
	}

	if slug := c.EffectiveHostSlug(); slug != "" {
		m.HostSlug = types.StringValue(slug)
	} else {
		m.HostSlug = types.StringNull()
	}

	if c.HostApplication != nil {
		m.HostAppStatus = types.StringValue(c.HostApplication.Status)
	} else {
		m.HostAppStatus = types.StringNull()
	}

	if len(c.Tags) > 0 {
		tags, d := types.SetValueFrom(ctx, types.StringType, c.Tags)
		diags.Append(d...)
		m.Tags = tags
	} else {
		m.Tags = types.SetNull(types.StringType)
	}

	linkType := types.ObjectType{AttrTypes: socialLinkAttrTypes}
	if len(c.SocialLinks) > 0 {
		links := make([]attr.Value, 0, len(c.SocialLinks))
		for _, l := range c.SocialLinks {
			obj, d := types.ObjectValue(socialLinkAttrTypes, map[string]attr.Value{
				"type": types.StringValue(l.Type),
				"url":  types.StringValue(l.URL),
			})
			diags.Append(d...)
			links = append(links, obj)
		}

		list, d := types.ListValue(linkType, links)
		diags.Append(d...)
		m.SocialLinks = list
	} else {
		m.SocialLinks = types.ListNull(linkType)
	}

	return diags
}
