// Package provider implements the Cosmoner Terraform provider.
package provider

import (
	"context"
	"os"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/datablock-dev/terraform-provider-cosmoner/internal/client"
)

// The environment variables the provider reads. They are the CLI's names, so
// a shell or CI job already set up for `cosmoner` works for Terraform too.
const (
	envAPIKey    = "COSMONER_API_KEY"
	envProjectID = "COSMONER_PROJECT_ID"
	envAPIURL    = "COSMONER_API_URL"
)

var _ provider.Provider = (*cosmonerProvider)(nil)

type cosmonerProvider struct {
	version string
}

type providerModel struct {
	APIKey     types.String `tfsdk:"api_key"`
	ProjectID  types.String `tfsdk:"project_id"`
	APIURL     types.String `tfsdk:"api_url"`
	MaxRetries types.Int64  `tfsdk:"max_retries"`
}

// providerData is what Configure hands every resource and data source.
type providerData struct {
	client *client.Client
	// projectID is the provider-level default, empty when none was given.
	projectID string
}

// New returns a constructor for the provider, as providerserver expects.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &cosmonerProvider{version: version}
	}
}

func (p *cosmonerProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "cosmoner"
	resp.Version = p.version
}

func (p *cosmonerProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages resources on [Cosmoner](https://cosmoner.com) through its public API.\n\n" +
			"Authenticate with a project API key created in the control panel. The key's scopes decide " +
			"which resources the provider can manage: each resource's page lists the scope it needs.",
		Attributes: map[string]schema.Attribute{
			"api_key": schema.StringAttribute{
				MarkdownDescription: "API key to authenticate with. Falls back to the `" + envAPIKey + "` environment " +
					"variable, which is the better place for it: a key in configuration ends up in version control.",
				Optional:  true,
				Sensitive: true,
			},
			"project_id": schema.StringAttribute{
				MarkdownDescription: "Project that resources are created in when they do not set `project_id` " +
					"themselves. Falls back to the `" + envProjectID + "` environment variable.",
				Optional: true,
			},
			"api_url": schema.StringAttribute{
				MarkdownDescription: "Base URL of the API. Falls back to the `" + envAPIURL + "` environment variable, " +
					"then to `" + client.DefaultBaseURL + "`.",
				Optional: true,
			},
			"max_retries": schema.Int64Attribute{
				MarkdownDescription: "How many times a request is retried after a rate limit, or after a server or " +
					"network failure on a request that is safe to repeat. Defaults to `" +
					strconv.Itoa(client.DefaultMaxRetries) + "`.",
				Optional:   true,
				Validators: []validator.Int64{int64validator.Between(0, 10)},
			},
		},
	}
}

func (p *cosmonerProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// A value that is only known after apply cannot configure a client the
	// plan itself needs, so say which one rather than failing obscurely later.
	for name, value := range map[string]types.String{"api_key": config.APIKey, "project_id": config.ProjectID, "api_url": config.APIURL} {
		if value.IsUnknown() {
			resp.Diagnostics.AddAttributeError(path.Root(name), "Unknown provider configuration",
				"The provider's "+name+" depends on a value that is not known until apply. "+
					"Set it to a known value, or use the environment variable instead.")
		}
	}
	if resp.Diagnostics.HasError() {
		return
	}

	apiKey := stringOrEnv(config.APIKey, envAPIKey)
	if apiKey == "" {
		resp.Diagnostics.AddAttributeError(path.Root("api_key"), "Missing API key",
			"Set the "+envAPIKey+" environment variable, or api_key in the provider block. "+
				"Keys are created under the project's API keys settings in the control panel.")
		return
	}

	maxRetries := client.DefaultMaxRetries
	if !config.MaxRetries.IsNull() && !config.MaxRetries.IsUnknown() {
		maxRetries = int(config.MaxRetries.ValueInt64())
	}

	c, err := client.New(client.Config{
		APIKey:     apiKey,
		BaseURL:    stringOrEnv(config.APIURL, envAPIURL),
		UserAgent:  "cosmoner-terraform/" + p.version,
		MaxRetries: maxRetries,
	})
	if err != nil {
		resp.Diagnostics.AddError("Invalid provider configuration", err.Error())
		return
	}

	data := &providerData{client: c, projectID: stringOrEnv(config.ProjectID, envProjectID)}
	resp.ResourceData = data
	resp.DataSourceData = data
}

func (p *cosmonerProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		newSecretResource,
		newVariableResource,
		newWebhookEndpointResource,
		newSSHKeyResource,
		newResourceGroupResource,
	}
}

func (p *cosmonerProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		newProjectDataSource,
	}
}

// stringOrEnv prefers the configured value, then the environment variable.
func stringOrEnv(value types.String, env string) string {
	if !value.IsNull() && !value.IsUnknown() && value.ValueString() != "" {
		return value.ValueString()
	}
	return os.Getenv(env)
}
