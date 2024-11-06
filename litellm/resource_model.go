package litellm

import (
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
)

func resourceLiteLLMModel() *schema.Resource {
	return &schema.Resource{
		Create: resourceLiteLLMModelCreate,
		Read:   resourceLiteLLMModelRead,
		Update: resourceLiteLLMModelUpdate,
		Delete: resourceLiteLLMModelDelete,

		Schema: map[string]*schema.Schema{
			"model_name": {
				Type:     schema.TypeString,
				Required: true,
			},
			"custom_llm_provider": {
				Type:     schema.TypeString,
				Required: true,
			},
			"tpm": {
				Type:     schema.TypeInt,
				Optional: true,
			},
			"rpm": {
				Type:     schema.TypeInt,
				Optional: true,
			},
			"model_api_key": {
				Type:      schema.TypeString,
				Optional:  true,
				Sensitive: true,
			},
			"model_api_base": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"api_version": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"base_model": {
				Type:     schema.TypeString,
				Required: true,
			},
			"tier": {
				Type:     schema.TypeString,
				Optional: true,
				Default:  "free",
			},
			"mode": {
				Type:     schema.TypeString,
				Optional: true,
				ValidateFunc: validation.StringInSlice([]string{
					"completion",
					"embeddings",
					"image_generation",
					"moderation",
					"audio_transcription",
				}, false),
			},
			"input_cost_per_million_tokens": {
				Type:     schema.TypeFloat,
				Optional: true,
				Default:  0.0,
			},
			"output_cost_per_million_tokens": {
				Type:     schema.TypeFloat,
				Optional: true,
				Default:  0.0,
			},
			"aws_access_key_id": {
				Type:      schema.TypeString,
				Optional:  true,
				Sensitive: true,
			},
			"aws_secret_access_key": {
				Type:      schema.TypeString,
				Optional:  true,
				Sensitive: true,
			},
			"aws_region_name": {
				Type:     schema.TypeString,
				Optional: true,
			},
		},
	}
}