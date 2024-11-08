package main

import (
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

// ProviderConfig holds the configuration for the LiteLLM provider
type ProviderConfig struct {
	APIBase string
	APIKey  string
}

// Provider returns a terraform.ResourceProvider.
func Provider() *schema.Provider {
	return &schema.Provider{
		ResourcesMap: map[string]*schema.Resource{
			"litellm_model": resourceLiteLLMModel(),
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

	return &config, nil
}
