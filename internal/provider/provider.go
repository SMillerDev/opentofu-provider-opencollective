// Copyright IBM Corp. 2021, 2026
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"os"

	"github.com/SMillerDev/opentofu-provider-opencollective/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const (
	// EnvEndpoint overrides the GraphQL endpoint when not set in configuration.
	EnvEndpoint = "OPENCOLLECTIVE_ENDPOINT"
	// EnvPersonalToken supplies the personal token when not set in configuration.
	EnvPersonalToken = "OPENCOLLECTIVE_PERSONAL_TOKEN"
)

// Ensure OpenCollectiveProvider satisfies various provider interfaces.
var _ provider.Provider = &OpenCollectiveProvider{}

// OpenCollectiveProvider defines the provider implementation.
type OpenCollectiveProvider struct {
	// version is set to the provider version on release, "dev" when the
	// provider is built and ran locally, and "test" when running acceptance
	// testing.
	version string
}

// OpenCollectiveProviderModel describes the provider data model.
type OpenCollectiveProviderModel struct {
	Endpoint      types.String `tfsdk:"endpoint"`
	PersonalToken types.String `tfsdk:"personal_token"`
}

func (p *OpenCollectiveProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "opencollective"
	resp.Version = p.version
}

func (p *OpenCollectiveProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"endpoint": schema.StringAttribute{
				MarkdownDescription: "Open Collective GraphQL API endpoint. Defaults to `" + client.DefaultEndpoint + "`. " +
					"Can also be set with the `" + EnvEndpoint + "` environment variable.",
				Optional: true,
			},
			"personal_token": schema.StringAttribute{
				MarkdownDescription: "Open Collective personal token, sent as the `Personal-Token` header. " +
					"Can also be set with the `" + EnvPersonalToken + "` environment variable. " +
					"Unauthenticated requests are limited to public data.",
				Optional:  true,
				Sensitive: true,
			},
		},
	}
}

func (p *OpenCollectiveProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var data OpenCollectiveProviderModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	if data.Endpoint.IsUnknown() {
		resp.Diagnostics.AddAttributeError(
			path.Root("endpoint"),
			"Unknown Open Collective API endpoint",
			"The provider cannot create the Open Collective API client as there is an unknown configuration value for the endpoint. "+
				"Either set the value statically in the configuration, or use the "+EnvEndpoint+" environment variable.",
		)
	}

	if data.PersonalToken.IsUnknown() {
		resp.Diagnostics.AddAttributeError(
			path.Root("personal_token"),
			"Unknown Open Collective personal token",
			"The provider cannot create the Open Collective API client as there is an unknown configuration value for the personal token. "+
				"Either set the value statically in the configuration, or use the "+EnvPersonalToken+" environment variable.",
		)
	}

	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := os.Getenv(EnvEndpoint)
	if !data.Endpoint.IsNull() {
		endpoint = data.Endpoint.ValueString()
	}

	token := os.Getenv(EnvPersonalToken)
	if !data.PersonalToken.IsNull() {
		token = data.PersonalToken.ValueString()
	}

	c := client.New(endpoint,
		client.WithToken(token),
		client.WithUserAgent("opentofu-provider-opencollective/"+p.version),
	)

	resp.DataSourceData = c
	resp.ResourceData = c
}

func (p *OpenCollectiveProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewCollectiveResource,
		NewCollectiveMemberResource,
	}
}

func (p *OpenCollectiveProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewCollectiveDataSource,
	}
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &OpenCollectiveProvider{
			version: version,
		}
	}
}
