package provider

import (
	"context"
	"crypto/tls"
	"net/http"
	"os"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure LiteLLMProvider satisfies various provider interfaces.
var _ provider.Provider = &LiteLLMProvider{}

// LiteLLMProvider defines the provider implementation.
type LiteLLMProvider struct {
	// version is set to the provider version on release, "dev" when the
	// provider is built and ran locally, and "test" when running acceptance
	// testing.
	version string
}

// LiteLLMProviderModel describes the provider data model.
type LiteLLMProviderModel struct {
	APIBase            types.String `tfsdk:"api_base"`
	APIKey             types.String `tfsdk:"api_key"`
	InsecureSkipVerify types.Bool   `tfsdk:"insecure_skip_verify"`
	LiteLLMChangedBy   types.String `tfsdk:"litellm_changed_by"`
	AdditionalHeaders  types.Map    `tfsdk:"additional_headers"`
}

// Client holds the HTTP client and configuration for API calls.
type Client struct {
	APIBase           string
	APIKey            string
	LiteLLMChangedBy  string
	HTTPClient        *http.Client
	AdditionalHeaders map[string]string
}

func (p *LiteLLMProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "litellm"
	resp.Version = p.version
}

func (p *LiteLLMProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Terraform provider for managing LiteLLM resources.",
		Attributes: map[string]schema.Attribute{
			"api_base": schema.StringAttribute{
				Description: "The base URL of the LiteLLM API. Can also be set via the LITELLM_API_BASE environment variable.",
				Optional:    true,
			},
			"api_key": schema.StringAttribute{
				Description: "The API key for authenticating with LiteLLM. Can also be set via the LITELLM_API_KEY environment variable.",
				Optional:    true,
				Sensitive:   true,
			},
			"insecure_skip_verify": schema.BoolAttribute{
				Description: "Skip TLS certificate verification. Defaults to false.",
				Optional:    true,
			},
			"litellm_changed_by": schema.StringAttribute{
				Description: "Value for the litellm-changed-by header to track actions performed by authorized users.",
				Optional:    true,
			},
			"additional_headers": schema.MapAttribute{
				Description: "Additional HTTP headers to set on requests to the LiteLLM API.",
				Optional:    true,
				ElementType: types.StringType,
			},
		},
	}
}

func (p *LiteLLMProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config LiteLLMProviderModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Use environment variables as fallback
	apiBase := os.Getenv("LITELLM_API_BASE")
	if !config.APIBase.IsNull() {
		apiBase = config.APIBase.ValueString()
	}

	apiKey := os.Getenv("LITELLM_API_KEY")
	if !config.APIKey.IsNull() {
		apiKey = config.APIKey.ValueString()
	}

	if apiBase == "" {
		resp.Diagnostics.AddError(
			"Missing API Base URL",
			"The provider cannot create the LiteLLM API client as there is a missing or empty value for the LiteLLM API base URL. "+
				"Set the api_base value in the configuration or use the LITELLM_API_BASE environment variable.",
		)
	}

	if apiKey == "" {
		resp.Diagnostics.AddError(
			"Missing API Key",
			"The provider cannot create the LiteLLM API client as there is a missing or empty value for the LiteLLM API key. "+
				"Set the api_key value in the configuration or use the LITELLM_API_KEY environment variable.",
		)
	}

	if resp.Diagnostics.HasError() {
		return
	}

	// Default insecure_skip_verify to false
	insecureSkipVerify := false
	if !config.InsecureSkipVerify.IsNull() {
		insecureSkipVerify = config.InsecureSkipVerify.ValueBool()
	}

	litellmChangedBy := ""
	if !config.LiteLLMChangedBy.IsNull() {
		litellmChangedBy = config.LiteLLMChangedBy.ValueString()
	}

	additionalHeaders := make(map[string]string)
	if !config.AdditionalHeaders.IsNull() {
		for key, value := range config.AdditionalHeaders.Elements() {
			additionalHeaders[key] = value.(types.String).ValueString()
		}
	}

	// Create HTTP client with TLS configuration
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: insecureSkipVerify},
	}

	client := &Client{
		APIBase:           apiBase,
		APIKey:            apiKey,
		LiteLLMChangedBy:  litellmChangedBy,
		AdditionalHeaders: additionalHeaders,
		HTTPClient: &http.Client{
			Transport: tr,
			Timeout:   30 * time.Second,
		},
	}

	resp.DataSourceData = client
	resp.ResourceData = client
}

func (p *LiteLLMProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewModelResource,
		NewKeyResource,
		NewKeyBlockResource,
		NewTeamResource,
		NewTeamBlockResource,
		NewTeamMemberResource,
		NewTeamMemberAddResource,
		NewMCPServerResource,
		NewCredentialResource,
		NewVectorStoreResource,
		NewOrganizationResource,
		NewOrganizationMemberResource,
		NewUserResource,
		NewBudgetResource,
		NewTagResource,
		NewAccessGroupResource,
		NewPromptResource,
		NewGuardrailResource,
		NewSearchToolResource,
	}
}

func (p *LiteLLMProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		// Single item lookups
		NewModelDataSource,
		NewKeyDataSource,
		NewTeamDataSource,
		NewCredentialDataSource,
		NewVectorStoreDataSource,
		NewOrganizationDataSource,
		NewUserDataSource,
		NewBudgetDataSource,
		NewTagDataSource,
		NewAccessGroupDataSource,
		NewPromptDataSource,
		NewGuardrailDataSource,
		NewMCPServerDataSource,
		NewSearchToolDataSource,
		// List data sources
		NewModelsListDataSource,
		NewKeysListDataSource,
		NewTeamsListDataSource,
		NewOrganizationsListDataSource,
		NewUsersListDataSource,
		NewBudgetsListDataSource,
		NewTagsListDataSource,
		NewAccessGroupsListDataSource,
		NewPromptsListDataSource,
		NewGuardrailsListDataSource,
		NewMCPServersListDataSource,
		NewSearchToolsListDataSource,
	}
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &LiteLLMProvider{
			version: version,
		}
	}
}
