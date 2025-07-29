package litellm

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
)

func resourceLiteLLMMonitoringConfig() *schema.Resource {
	return &schema.Resource{
		CreateContext: resourceLiteLLMMonitoringConfigCreate,
		ReadContext:   resourceLiteLLMMonitoringConfigRead,
		UpdateContext: resourceLiteLLMMonitoringConfigUpdate,
		DeleteContext: resourceLiteLLMMonitoringConfigDelete,
		Importer: &schema.ResourceImporter{
			StateContext: schema.ImportStatePassthroughContext,
		},
		Description: "Manages monitoring and callback configurations in LiteLLM for metrics and observability",
		Schema: map[string]*schema.Schema{
			"callback_type": {
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: validation.StringInSlice([]string{"prometheus", "datadog", "otel", "langfuse", "custom_webhook"}, false),
				Description:  "Type of monitoring callback (prometheus, datadog, otel, langfuse, custom_webhook)",
			},
			"endpoint": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "Endpoint URL for the monitoring service",
			},
			"enabled": {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     true,
				Description: "Whether this monitoring configuration is enabled",
			},
			"enabled_metrics": {
				Type:        schema.TypeSet,
				Optional:    true,
				Description: "List of metrics to enable (requests, latency, errors, costs, tokens)",
				Elem: &schema.Schema{
					Type: schema.TypeString,
					ValidateFunc: validation.StringInSlice([]string{
						"requests", "latency", "errors", "costs", "tokens",
						"model_usage", "user_usage", "team_usage", "key_usage",
					}, false),
				},
			},
			"labels": {
				Type:        schema.TypeMap,
				Optional:    true,
				Description: "Labels to add to all metrics",
				Elem: &schema.Schema{
					Type: schema.TypeString,
				},
			},
			"sampling_rate": {
				Type:         schema.TypeFloat,
				Optional:     true,
				Default:      1.0,
				ValidateFunc: validation.FloatBetween(0, 1),
				Description:  "Sampling rate for metrics (0.0 to 1.0)",
			},
			"batch_size": {
				Type:         schema.TypeInt,
				Optional:     true,
				Default:      100,
				ValidateFunc: validation.IntBetween(1, 10000),
				Description:  "Batch size for metric exports",
			},
			"flush_interval": {
				Type:         schema.TypeInt,
				Optional:     true,
				Default:      60,
				ValidateFunc: validation.IntBetween(1, 3600),
				Description:  "Flush interval in seconds",
			},
			"auth_config": {
				Type:        schema.TypeMap,
				Optional:    true,
				Sensitive:   true,
				Description: "Authentication configuration for the monitoring endpoint",
				Elem: &schema.Schema{
					Type: schema.TypeString,
				},
			},
			"custom_headers": {
				Type:        schema.TypeMap,
				Optional:    true,
				Description: "Custom headers to include in monitoring requests",
				Elem: &schema.Schema{
					Type: schema.TypeString,
				},
			},
			"metric_prefix": {
				Type:        schema.TypeString,
				Optional:    true,
				Default:     "litellm",
				Description: "Prefix for all metric names",
			},
			"include_request_details": {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
				Description: "Include detailed request information in metrics",
			},
			"include_response_details": {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
				Description: "Include detailed response information in metrics",
			},
		},
	}
}

func resourceLiteLLMMonitoringConfigCreate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	client := m.(*Client)
	
	callbackType := d.Get("callback_type").(string)
	
	// Build the monitoring configuration
	config := map[string]interface{}{
		"callback_type":            callbackType,
		"enabled":                  d.Get("enabled").(bool),
		"sampling_rate":            d.Get("sampling_rate").(float64),
		"batch_size":               d.Get("batch_size").(int),
		"flush_interval":           d.Get("flush_interval").(int),
		"metric_prefix":            d.Get("metric_prefix").(string),
		"include_request_details":  d.Get("include_request_details").(bool),
		"include_response_details": d.Get("include_response_details").(bool),
	}
	
	// Add optional fields
	if v, ok := d.GetOk("endpoint"); ok {
		config["endpoint"] = v.(string)
	}
	
	if v, ok := d.GetOk("enabled_metrics"); ok {
		metrics := v.(*schema.Set).List()
		config["enabled_metrics"] = metrics
	}
	
	if v, ok := d.GetOk("labels"); ok {
		config["labels"] = v.(map[string]interface{})
	}
	
	if v, ok := d.GetOk("auth_config"); ok {
		config["auth_config"] = v.(map[string]interface{})
	}
	
	if v, ok := d.GetOk("custom_headers"); ok {
		config["custom_headers"] = v.(map[string]interface{})
	}
	
	// Create the monitoring configuration
	response, err := client.CreateMonitoringConfig(config)
	if err != nil {
		return diag.Errorf("Error creating monitoring configuration: %s", err)
	}
	
	// Extract the ID from the response
	if id, ok := response["config_id"].(string); ok {
		d.SetId(id)
	} else {
		d.SetId(fmt.Sprintf("monitoring-%s", callbackType))
	}
	
	log.Printf("[INFO] Created monitoring configuration with ID: %s", d.Id())
	
	return resourceLiteLLMMonitoringConfigRead(ctx, d, m)
}

func resourceLiteLLMMonitoringConfigRead(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	client := m.(*Client)
	
	config, err := client.GetMonitoringConfig(d.Id())
	if err != nil {
		if err.Error() == "monitoring configuration not found" {
			log.Printf("[WARN] Monitoring configuration %s not found, removing from state", d.Id())
			d.SetId("")
			return nil
		}
		return diag.Errorf("Error reading monitoring configuration: %s", err)
	}
	
	// Set the attributes
	d.Set("callback_type", config["callback_type"])
	d.Set("endpoint", config["endpoint"])
	d.Set("enabled", config["enabled"])
	d.Set("sampling_rate", config["sampling_rate"])
	d.Set("batch_size", config["batch_size"])
	d.Set("flush_interval", config["flush_interval"])
	d.Set("metric_prefix", config["metric_prefix"])
	d.Set("include_request_details", config["include_request_details"])
	d.Set("include_response_details", config["include_response_details"])
	
	if metrics, ok := config["enabled_metrics"].([]interface{}); ok {
		d.Set("enabled_metrics", metrics)
	}
	
	if labels, ok := config["labels"].(map[string]interface{}); ok {
		d.Set("labels", labels)
	}
	
	// Note: We don't read back auth_config for security reasons
	
	if headers, ok := config["custom_headers"].(map[string]interface{}); ok {
		d.Set("custom_headers", headers)
	}
	
	return nil
}

func resourceLiteLLMMonitoringConfigUpdate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	client := m.(*Client)
	
	// Build the updated configuration
	config := map[string]interface{}{
		"enabled":                  d.Get("enabled").(bool),
		"sampling_rate":            d.Get("sampling_rate").(float64),
		"batch_size":               d.Get("batch_size").(int),
		"flush_interval":           d.Get("flush_interval").(int),
		"metric_prefix":            d.Get("metric_prefix").(string),
		"include_request_details":  d.Get("include_request_details").(bool),
		"include_response_details": d.Get("include_response_details").(bool),
	}
	
	// Add optional fields that can be updated
	if v, ok := d.GetOk("endpoint"); ok {
		config["endpoint"] = v.(string)
	}
	
	if d.HasChange("enabled_metrics") {
		if v, ok := d.GetOk("enabled_metrics"); ok {
			metrics := v.(*schema.Set).List()
			config["enabled_metrics"] = metrics
		}
	}
	
	if d.HasChange("labels") {
		if v, ok := d.GetOk("labels"); ok {
			config["labels"] = v.(map[string]interface{})
		}
	}
	
	if d.HasChange("auth_config") {
		if v, ok := d.GetOk("auth_config"); ok {
			config["auth_config"] = v.(map[string]interface{})
		}
	}
	
	if d.HasChange("custom_headers") {
		if v, ok := d.GetOk("custom_headers"); ok {
			config["custom_headers"] = v.(map[string]interface{})
		}
	}
	
	// Update the monitoring configuration
	_, err := client.UpdateMonitoringConfig(d.Id(), config)
	if err != nil {
		return diag.Errorf("Error updating monitoring configuration: %s", err)
	}
	
	log.Printf("[INFO] Updated monitoring configuration with ID: %s", d.Id())
	
	return resourceLiteLLMMonitoringConfigRead(ctx, d, m)
}

func resourceLiteLLMMonitoringConfigDelete(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	client := m.(*Client)
	
	err := client.DeleteMonitoringConfig(d.Id())
	if err != nil {
		return diag.Errorf("Error deleting monitoring configuration: %s", err)
	}
	
	log.Printf("[INFO] Deleted monitoring configuration with ID: %s", d.Id())
	
	return nil
}

// Client methods for monitoring configuration management
func (c *Client) CreateMonitoringConfig(config map[string]interface{}) (map[string]interface{}, error) {
	body, _ := json.Marshal(config)
	return c.doRequest("POST", "/monitoring/config/new", body)
}

func (c *Client) GetMonitoringConfig(configID string) (map[string]interface{}, error) {
	return c.doRequest("GET", fmt.Sprintf("/monitoring/config/%s", configID), nil)
}

func (c *Client) UpdateMonitoringConfig(configID string, config map[string]interface{}) (map[string]interface{}, error) {
	body, _ := json.Marshal(config)
	return c.doRequest("PUT", fmt.Sprintf("/monitoring/config/%s", configID), body)
}

func (c *Client) DeleteMonitoringConfig(configID string) error {
	_, err := c.doRequest("DELETE", fmt.Sprintf("/monitoring/config/%s", configID), nil)
	return err
}