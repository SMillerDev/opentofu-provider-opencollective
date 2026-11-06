// Copyright IBM Corp. 2021, 2026
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/SMillerDev/opentofu-provider-opencollective/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var (
	_ datasource.DataSource                     = &CollectiveDataSource{}
	_ datasource.DataSourceWithConfigure        = &CollectiveDataSource{}
	_ datasource.DataSourceWithConfigValidators = &CollectiveDataSource{}
)

func NewCollectiveDataSource() datasource.DataSource {
	return &CollectiveDataSource{}
}

// CollectiveDataSource looks up a single Open Collective collective.
type CollectiveDataSource struct {
	client *client.Client
}

// CollectiveDataSourceModel describes the data source data model.
type CollectiveDataSourceModel struct {
	ID              types.String `tfsdk:"id"`
	Slug            types.String `tfsdk:"slug"`
	GithubHandle    types.String `tfsdk:"github_handle"`
	LegacyID        types.Int64  `tfsdk:"legacy_id"`
	Type            types.String `tfsdk:"type"`
	Name            types.String `tfsdk:"name"`
	Description     types.String `tfsdk:"description"`
	LongDescription types.String `tfsdk:"long_description"`
	Website         types.String `tfsdk:"website"`
	ImageURL        types.String `tfsdk:"image_url"`
	Currency        types.String `tfsdk:"currency"`
	Tags            types.List   `tfsdk:"tags"`
	SocialLinks     types.List   `tfsdk:"social_links"`
	IsActive        types.Bool   `tfsdk:"is_active"`
	IsApproved      types.Bool   `tfsdk:"is_approved"`
	IsArchived      types.Bool   `tfsdk:"is_archived"`
	CreatedAt       types.String `tfsdk:"created_at"`
	HostID          types.String `tfsdk:"host_id"`
	HostSlug        types.String `tfsdk:"host_slug"`
}

var socialLinkAttrTypes = map[string]attr.Type{
	"type": types.StringType,
	"url":  types.StringType,
}

func (d *CollectiveDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_collective"
}

func (d *CollectiveDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up an Open Collective collective by `id`, `slug` or `github_handle`. Exactly one of these must be set.",

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Public identifier of the collective.",
				Optional:            true,
				Computed:            true,
			},
			"slug": schema.StringAttribute{
				MarkdownDescription: "Slug of the collective, as used in its URL (e.g. `babel` for `https://opencollective.com/babel`).",
				Optional:            true,
				Computed:            true,
			},
			"github_handle": schema.StringAttribute{
				MarkdownDescription: "GitHub handle (`org` or `org/repo`) linked to the collective.",
				Optional:            true,
			},
			"legacy_id": schema.Int64Attribute{
				MarkdownDescription: "Legacy numeric identifier of the collective.",
				Computed:            true,
			},
			"type": schema.StringAttribute{
				MarkdownDescription: "Account type, e.g. `COLLECTIVE`.",
				Computed:            true,
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Public name of the collective.",
				Computed:            true,
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "Short description of the collective.",
				Computed:            true,
			},
			"long_description": schema.StringAttribute{
				MarkdownDescription: "Long description of the collective (HTML).",
				Computed:            true,
			},
			"website": schema.StringAttribute{
				MarkdownDescription: "Website of the collective.",
				Computed:            true,
			},
			"image_url": schema.StringAttribute{
				MarkdownDescription: "URL of the collective's logo.",
				Computed:            true,
			},
			"currency": schema.StringAttribute{
				MarkdownDescription: "Currency code of the collective, e.g. `USD`.",
				Computed:            true,
			},
			"tags": schema.ListAttribute{
				MarkdownDescription: "Tags assigned to the collective.",
				ElementType:         types.StringType,
				Computed:            true,
			},
			"social_links": schema.ListNestedAttribute{
				MarkdownDescription: "Links to the collective's profiles on other platforms.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"type": schema.StringAttribute{
							MarkdownDescription: "Platform type, e.g. `GITHUB` or `WEBSITE`.",
							Computed:            true,
						},
						"url": schema.StringAttribute{
							MarkdownDescription: "URL of the profile.",
							Computed:            true,
						},
					},
				},
			},
			"is_active": schema.BoolAttribute{
				MarkdownDescription: "Whether the collective is active (has a host and can receive contributions).",
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
			},
			"host_id": schema.StringAttribute{
				MarkdownDescription: "Public identifier of the fiscal host, if any.",
				Computed:            true,
			},
			"host_slug": schema.StringAttribute{
				MarkdownDescription: "Slug of the fiscal host, if any.",
				Computed:            true,
			},
		},
	}
}

func (d *CollectiveDataSource) ConfigValidators(ctx context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{
		datasourcevalidator.ExactlyOneOf(
			path.MatchRoot("id"),
			path.MatchRoot("slug"),
			path.MatchRoot("github_handle"),
		),
	}
}

func (d *CollectiveDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *client.Client, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)

		return
	}

	d.client = c
}

func (d *CollectiveDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data CollectiveDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	lookup := client.CollectiveLookup{
		ID:           data.ID.ValueString(),
		Slug:         data.Slug.ValueString(),
		GithubHandle: data.GithubHandle.ValueString(),
	}

	tflog.Debug(ctx, "reading collective", map[string]any{
		"id": lookup.ID, "slug": lookup.Slug, "github_handle": lookup.GithubHandle,
	})

	collective, err := d.client.GetCollective(ctx, lookup)
	if errors.Is(err, client.ErrNotFound) {
		resp.Diagnostics.AddError("Collective not found", fmt.Sprintf("No collective matched %s.", describeLookup(lookup)))
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read collective", fmt.Sprintf("Looking up collective by %s failed: %s", describeLookup(lookup), err))
		return
	}

	// Preserve configured lookup values exactly as written; only fill them in
	// when they were not part of the configuration.
	if data.ID.IsNull() {
		data.ID = types.StringValue(collective.ID)
	}
	if data.Slug.IsNull() {
		data.Slug = types.StringValue(collective.Slug)
	}

	data.LegacyID = types.Int64Value(collective.LegacyID)
	data.Type = types.StringValue(collective.Type)
	data.Name = types.StringPointerValue(collective.Name)
	data.Description = types.StringPointerValue(collective.Description)
	data.LongDescription = types.StringPointerValue(collective.LongDescription)
	data.Website = types.StringPointerValue(collective.Website)
	data.ImageURL = types.StringPointerValue(collective.ImageURL)
	data.Currency = types.StringValue(collective.Currency)
	data.IsActive = types.BoolValue(collective.IsActive)
	data.IsApproved = types.BoolValue(collective.IsApproved)
	data.IsArchived = types.BoolValue(collective.IsArchived)
	data.CreatedAt = types.StringPointerValue(collective.CreatedAt)

	if collective.Host != nil {
		data.HostID = types.StringValue(collective.Host.ID)
		data.HostSlug = types.StringValue(collective.Host.Slug)
	} else {
		data.HostID = types.StringNull()
		data.HostSlug = types.StringNull()
	}

	tags, diags := types.ListValueFrom(ctx, types.StringType, nonNilStrings(collective.Tags))
	resp.Diagnostics.Append(diags...)
	data.Tags = tags

	links := make([]attr.Value, 0, len(collective.SocialLinks))
	for _, l := range collective.SocialLinks {
		obj, diags := types.ObjectValue(socialLinkAttrTypes, map[string]attr.Value{
			"type": types.StringValue(l.Type),
			"url":  types.StringValue(l.URL),
		})
		resp.Diagnostics.Append(diags...)
		links = append(links, obj)
	}
	socialLinks, diags := types.ListValue(types.ObjectType{AttrTypes: socialLinkAttrTypes}, links)
	resp.Diagnostics.Append(diags...)
	data.SocialLinks = socialLinks

	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func describeLookup(l client.CollectiveLookup) string {
	switch {
	case l.ID != "":
		return fmt.Sprintf("id %q", l.ID)
	case l.Slug != "":
		return fmt.Sprintf("slug %q", l.Slug)
	default:
		return fmt.Sprintf("github_handle %q", l.GithubHandle)
	}
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}

	return s
}
