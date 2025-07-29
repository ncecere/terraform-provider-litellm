package litellm

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func resourceLiteLLMModelConfig() *schema.Resource {
	return &schema.Resource{
		CreateContext: resourceLiteLLMModelConfigCreate,
		ReadContext:   resourceLiteLLMModelConfigRead,
		UpdateContext: resourceLiteLLMModelConfigUpdate,
		DeleteContext: resourceLiteLLMModelConfigDelete,
		Importer: &schema.ResourceImporter{
			StateContext: schema.ImportStatePassthroughContext,
		},
		Description: "Manages model configurations in LiteLLM for routing and parameter settings",
		Schema: map[string]*schema.Schema{
			"model_name": {
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
				Description: "Name of the model configuration",
			},
			"litellm_params": {
				Type:        schema.TypeMap,
				Required:    true,
				Description: "LiteLLM parameters for the model",
				Elem: &schema.Schema{
					Type: schema.TypeString,
				},
			},
			"model_info": {
				Type:        schema.TypeMap,
				Optional:    true,
				Description: "Additional model information and capabilities",
				Elem: &schema.Schema{
					Type: schema.TypeString,
				},
			},
			"enabled": {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     true,
				Description: "Whether this model configuration is enabled",
			},
			"priority": {
				Type:        schema.TypeInt,
				Optional:    true,
				Default:     0,
				Description: "Priority for model selection (higher values preferred)",
			},
			"rpm_limit": {
				Type:        schema.TypeInt,
				Optional:    true,
				Description: "Requests per minute limit for this model",
			},
			"tpm_limit": {
				Type:        schema.TypeInt,
				Optional:    true,
				Description: "Tokens per minute limit for this model",
			},
		},
	}
}

func resourceLiteLLMModelConfigCreate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	client := m.(*Client)
	
	modelName := d.Get("model_name").(string)
	
	// Build the model configuration
	modelConfig := map[string]interface{}{
		"model_name": modelName,
		"enabled":    d.Get("enabled").(bool),
		"priority":   d.Get("priority").(int),
	}
	
	// Add litellm_params
	if v, ok := d.GetOk("litellm_params"); ok {
		modelConfig["litellm_params"] = v.(map[string]interface{})
	}
	
	// Add model_info
	if v, ok := d.GetOk("model_info"); ok {
		modelConfig["model_info"] = v.(map[string]interface{})
	}
	
	// Add rate limits
	if v, ok := d.GetOk("rpm_limit"); ok {
		modelConfig["rpm"] = v.(int)
	}
	
	if v, ok := d.GetOk("tpm_limit"); ok {
		modelConfig["tpm"] = v.(int)
	}
	
	// Create the model configuration
	response, err := client.CreateModelConfig(modelConfig)
	if err != nil {
		return diag.Errorf("Error creating model configuration: %s", err)
	}
	
	// Extract the ID from the response
	if id, ok := response["id"].(string); ok {
		d.SetId(id)
	} else {
		d.SetId(modelName)
	}
	
	log.Printf("[INFO] Created model configuration with ID: %s", d.Id())
	
	return resourceLiteLLMModelConfigRead(ctx, d, m)
}

func resourceLiteLLMModelConfigRead(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	client := m.(*Client)
	
	modelConfig, err := client.GetModelConfig(d.Id())
	if err != nil {
		if err.Error() == "model configuration not found" {
			log.Printf("[WARN] Model configuration %s not found, removing from state", d.Id())
			d.SetId("")
			return nil
		}
		return diag.Errorf("Error reading model configuration: %s", err)
	}
	
	// Set the attributes
	d.Set("model_name", modelConfig["model_name"])
	d.Set("enabled", modelConfig["enabled"])
	d.Set("priority", modelConfig["priority"])
	
	if params, ok := modelConfig["litellm_params"].(map[string]interface{}); ok {
		d.Set("litellm_params", params)
	}
	
	if info, ok := modelConfig["model_info"].(map[string]interface{}); ok {
		d.Set("model_info", info)
	}
	
	if rpm, ok := modelConfig["rpm"].(float64); ok {
		d.Set("rpm_limit", int(rpm))
	}
	
	if tpm, ok := modelConfig["tpm"].(float64); ok {
		d.Set("tpm_limit", int(tpm))
	}
	
	return nil
}

func resourceLiteLLMModelConfigUpdate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	client := m.(*Client)
	
	// Build the updated model configuration
	modelConfig := map[string]interface{}{
		"model_name": d.Get("model_name").(string),
		"enabled":    d.Get("enabled").(bool),
		"priority":   d.Get("priority").(int),
	}
	
	// Add litellm_params
	if v, ok := d.GetOk("litellm_params"); ok {
		modelConfig["litellm_params"] = v.(map[string]interface{})
	}
	
	// Add model_info
	if v, ok := d.GetOk("model_info"); ok {
		modelConfig["model_info"] = v.(map[string]interface{})
	}
	
	// Add rate limits
	if v, ok := d.GetOk("rpm_limit"); ok {
		modelConfig["rpm"] = v.(int)
	}
	
	if v, ok := d.GetOk("tpm_limit"); ok {
		modelConfig["tpm"] = v.(int)
	}
	
	// Update the model configuration
	_, err := client.UpdateModelConfig(d.Id(), modelConfig)
	if err != nil {
		return diag.Errorf("Error updating model configuration: %s", err)
	}
	
	log.Printf("[INFO] Updated model configuration with ID: %s", d.Id())
	
	return resourceLiteLLMModelConfigRead(ctx, d, m)
}

func resourceLiteLLMModelConfigDelete(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	client := m.(*Client)
	
	err := client.DeleteModelConfig(d.Id())
	if err != nil {
		return diag.Errorf("Error deleting model configuration: %s", err)
	}
	
	log.Printf("[INFO] Deleted model configuration with ID: %s", d.Id())
	
	return nil
}

// Client methods for model configuration management
func (c *Client) CreateModelConfig(config map[string]interface{}) (map[string]interface{}, error) {
	body, _ := json.Marshal(config)
	return c.doRequest("POST", "/config/model/new", body)
}

func (c *Client) GetModelConfig(modelID string) (map[string]interface{}, error) {
	return c.doRequest("GET", fmt.Sprintf("/config/model/%s", modelID), nil)
}

func (c *Client) UpdateModelConfig(modelID string, config map[string]interface{}) (map[string]interface{}, error) {
	body, _ := json.Marshal(config)
	return c.doRequest("PUT", fmt.Sprintf("/config/model/%s", modelID), body)
}

func (c *Client) DeleteModelConfig(modelID string) error {
	_, err := c.doRequest("DELETE", fmt.Sprintf("/config/model/%s", modelID), nil)
	return err
}