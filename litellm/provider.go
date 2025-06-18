package litellm

import (
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

// Provider returns a terraform.ResourceProvider.
func Provider() *schema.Provider {
	return &schema.Provider{
		ResourcesMap: map[string]*schema.Resource{
			"litellm_model":           resourceLiteLLMModel(),
			"litellm_team":            ResourceLiteLLMTeam(),
			"litellm_team_member":     resourceLiteLLMTeamMember(),
			"litellm_team_member_add": resourceLiteLLMTeamMemberAdd(),
			"litellm_key":             resourceKey(),
			"litellm_user":            ResourceLiteLLMUser(),
		},
		Schema: map[string]*schema.Schema{
			"api_base": {
				Type:        schema.TypeString,
				Required:    true,
				Sensitive:   false,
				DefaultFunc: schema.EnvDefaultFunc("LITELLM_API_BASE", nil),
				Description: "The base URL of the LiteLLM API",
			},
			"api_key": {
				Type:        schema.TypeString,
				Required:    true,
				Sensitive:   true,
				DefaultFunc: schema.EnvDefaultFunc("LITELLM_API_KEY", nil),
				Description: "The API key for authenticating with LiteLLM",
			},
		},
		ConfigureFunc: providerConfigure,
	}
}

// providerConfigure configures the provider with the given schema data.
func providerConfigure(d *schema.ResourceData) (interface{}, error) {
	config := ProviderConfig{
		APIBase: d.Get("api_base").(string),
		APIKey:  d.Get("api_key").(string),
	}

	return NewClient(config.APIBase, config.APIKey), nil
}
