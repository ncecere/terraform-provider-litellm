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

func resourceLiteLLMRouterConfig() *schema.Resource {
	return &schema.Resource{
		CreateContext: resourceLiteLLMRouterConfigCreate,
		ReadContext:   resourceLiteLLMRouterConfigRead,
		UpdateContext: resourceLiteLLMRouterConfigUpdate,
		DeleteContext: resourceLiteLLMRouterConfigDelete,
		Importer: &schema.ResourceImporter{
			StateContext: schema.ImportStatePassthroughContext,
		},
		Description: "Manages router configurations in LiteLLM for load balancing and failover strategies",
		Schema: map[string]*schema.Schema{
			"routing_strategy": {
				Type:         schema.TypeString,
				Required:     true,
				ValidateFunc: validation.StringInSlice([]string{"simple-shuffle", "least-busy", "usage-based-routing", "latency-based-routing", "cost-based-routing"}, false),
				Description:  "Routing strategy for load balancing",
			},
			"model_aliases": {
				Type:        schema.TypeMap,
				Optional:    true,
				Description: "Map of model aliases to actual model names",
				Elem: &schema.Schema{
					Type: schema.TypeString,
				},
			},
			"fallback_chains": {
				Type:        schema.TypeMap,
				Optional:    true,
				Description: "Map of model names to their fallback chains (comma-separated list)",
				Elem: &schema.Schema{
					Type: schema.TypeString,
				},
			},
			"enabled": {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     true,
				Description: "Whether this router configuration is enabled",
			},
			"retry_config": {
				Type:        schema.TypeList,
				Optional:    true,
				MaxItems:    1,
				Description: "Retry configuration for failed requests",
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"num_retries": {
							Type:         schema.TypeInt,
							Optional:     true,
							Default:      3,
							ValidateFunc: validation.IntBetween(0, 10),
							Description:  "Number of retry attempts",
						},
						"retry_after": {
							Type:         schema.TypeInt,
							Optional:     true,
							Default:      5,
							ValidateFunc: validation.IntBetween(1, 300),
							Description:  "Seconds to wait between retries",
						},
						"retry_on_status_codes": {
							Type:        schema.TypeSet,
							Optional:    true,
							Description: "HTTP status codes that trigger retries",
							Elem: &schema.Schema{
								Type: schema.TypeInt,
							},
						},
					},
				},
			},
			"timeout_config": {
				Type:        schema.TypeList,
				Optional:    true,
				MaxItems:    1,
				Description: "Timeout configuration for requests",
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"request_timeout": {
							Type:         schema.TypeInt,
							Optional:     true,
							Default:      600,
							ValidateFunc: validation.IntBetween(1, 3600),
							Description:  "Request timeout in seconds",
						},
						"stream_timeout": {
							Type:         schema.TypeInt,
							Optional:     true,
							Default:      1800,
							ValidateFunc: validation.IntBetween(1, 7200),
							Description:  "Stream timeout in seconds",
						},
					},
				},
			},
			"load_balancing_config": {
				Type:        schema.TypeList,
				Optional:    true,
				MaxItems:    1,
				Description: "Load balancing configuration",
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"enable_health_checks": {
							Type:        schema.TypeBool,
							Optional:    true,
							Default:     true,
							Description: "Enable health checks for models",
						},
						"health_check_interval": {
							Type:         schema.TypeInt,
							Optional:     true,
							Default:      30,
							ValidateFunc: validation.IntBetween(10, 300),
							Description:  "Health check interval in seconds",
						},
						"failure_threshold": {
							Type:         schema.TypeInt,
							Optional:     true,
							Default:      3,
							ValidateFunc: validation.IntBetween(1, 10),
							Description:  "Number of failures before marking unhealthy",
						},
						"success_threshold": {
							Type:         schema.TypeInt,
							Optional:     true,
							Default:      2,
							ValidateFunc: validation.IntBetween(1, 10),
							Description:  "Number of successes before marking healthy",
						},
					},
				},
			},
			"cache_config": {
				Type:        schema.TypeList,
				Optional:    true,
				MaxItems:    1,
				Description: "Caching configuration",
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"enable_cache": {
							Type:        schema.TypeBool,
							Optional:    true,
							Default:     false,
							Description: "Enable response caching",
						},
						"cache_ttl": {
							Type:         schema.TypeInt,
							Optional:     true,
							Default:      3600,
							ValidateFunc: validation.IntBetween(60, 86400),
							Description:  "Cache TTL in seconds",
						},
						"cache_size_mb": {
							Type:         schema.TypeInt,
							Optional:     true,
							Default:      100,
							ValidateFunc: validation.IntBetween(10, 10000),
							Description:  "Maximum cache size in MB",
						},
					},
				},
			},
			"rate_limit_config": {
				Type:        schema.TypeList,
				Optional:    true,
				MaxItems:    1,
				Description: "Rate limiting configuration",
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"enable_rate_limiting": {
							Type:        schema.TypeBool,
							Optional:    true,
							Default:     false,
							Description: "Enable rate limiting",
						},
						"requests_per_minute": {
							Type:         schema.TypeInt,
							Optional:     true,
							Default:      1000,
							ValidateFunc: validation.IntBetween(1, 100000),
							Description:  "Requests per minute limit",
						},
						"tokens_per_minute": {
							Type:         schema.TypeInt,
							Optional:     true,
							Default:      100000,
							ValidateFunc: validation.IntBetween(1, 10000000),
							Description:  "Tokens per minute limit",
						},
					},
				},
			},
		},
	}
}

func resourceLiteLLMRouterConfigCreate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	client := m.(*Client)
	
	// Build the router configuration
	config := map[string]interface{}{
		"routing_strategy": d.Get("routing_strategy").(string),
		"enabled":          d.Get("enabled").(bool),
	}
	
	// Add model aliases
	if v, ok := d.GetOk("model_aliases"); ok {
		config["model_aliases"] = v.(map[string]interface{})
	}
	
	// Process fallback chains (convert comma-separated strings to arrays)
	if v, ok := d.GetOk("fallback_chains"); ok {
		fallbackMap := v.(map[string]interface{})
		processedFallbacks := make(map[string][]string)
		for model, fallbacks := range fallbackMap {
			fallbackStr := fallbacks.(string)
			fallbackList := []string{}
			for _, fb := range splitAndTrim(fallbackStr, ",") {
				if fb != "" {
					fallbackList = append(fallbackList, fb)
				}
			}
			processedFallbacks[model] = fallbackList
		}
		config["fallback_chains"] = processedFallbacks
	}
	
	// Add retry configuration
	if v, ok := d.GetOk("retry_config"); ok {
		retryList := v.([]interface{})
		if len(retryList) > 0 {
			retryConfig := retryList[0].(map[string]interface{})
			config["retry_config"] = retryConfig
		}
	}
	
	// Add timeout configuration
	if v, ok := d.GetOk("timeout_config"); ok {
		timeoutList := v.([]interface{})
		if len(timeoutList) > 0 {
			timeoutConfig := timeoutList[0].(map[string]interface{})
			config["timeout_config"] = timeoutConfig
		}
	}
	
	// Add load balancing configuration
	if v, ok := d.GetOk("load_balancing_config"); ok {
		lbList := v.([]interface{})
		if len(lbList) > 0 {
			lbConfig := lbList[0].(map[string]interface{})
			config["load_balancing_config"] = lbConfig
		}
	}
	
	// Add cache configuration
	if v, ok := d.GetOk("cache_config"); ok {
		cacheList := v.([]interface{})
		if len(cacheList) > 0 {
			cacheConfig := cacheList[0].(map[string]interface{})
			config["cache_config"] = cacheConfig
		}
	}
	
	// Add rate limit configuration
	if v, ok := d.GetOk("rate_limit_config"); ok {
		rlList := v.([]interface{})
		if len(rlList) > 0 {
			rlConfig := rlList[0].(map[string]interface{})
			config["rate_limit_config"] = rlConfig
		}
	}
	
	// Create the router configuration
	response, err := client.CreateRouterConfig(config)
	if err != nil {
		return diag.Errorf("Error creating router configuration: %s", err)
	}
	
	// Extract the ID from the response
	if id, ok := response["router_id"].(string); ok {
		d.SetId(id)
	} else {
		d.SetId(fmt.Sprintf("router-%s", d.Get("routing_strategy").(string)))
	}
	
	log.Printf("[INFO] Created router configuration with ID: %s", d.Id())
	
	return resourceLiteLLMRouterConfigRead(ctx, d, m)
}

func resourceLiteLLMRouterConfigRead(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	client := m.(*Client)
	
	config, err := client.GetRouterConfig(d.Id())
	if err != nil {
		if err.Error() == "router configuration not found" {
			log.Printf("[WARN] Router configuration %s not found, removing from state", d.Id())
			d.SetId("")
			return nil
		}
		return diag.Errorf("Error reading router configuration: %s", err)
	}
	
	// Set basic attributes
	d.Set("routing_strategy", config["routing_strategy"])
	d.Set("enabled", config["enabled"])
	
	if aliases, ok := config["model_aliases"].(map[string]interface{}); ok {
		d.Set("model_aliases", aliases)
	}
	
	// Convert fallback chains back to comma-separated strings
	if fallbacks, ok := config["fallback_chains"].(map[string]interface{}); ok {
		processedFallbacks := make(map[string]string)
		for model, fallbackList := range fallbacks {
			if list, ok := fallbackList.([]interface{}); ok {
				fallbackStrs := make([]string, len(list))
				for i, fb := range list {
					fallbackStrs[i] = fb.(string)
				}
				processedFallbacks[model] = joinStrings(fallbackStrs, ",")
			}
		}
		d.Set("fallback_chains", processedFallbacks)
	}
	
	// Set complex configurations
	setComplexConfig(d, "retry_config", config)
	setComplexConfig(d, "timeout_config", config)
	setComplexConfig(d, "load_balancing_config", config)
	setComplexConfig(d, "cache_config", config)
	setComplexConfig(d, "rate_limit_config", config)
	
	return nil
}

func resourceLiteLLMRouterConfigUpdate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	client := m.(*Client)
	
	// Build the updated configuration
	config := map[string]interface{}{
		"routing_strategy": d.Get("routing_strategy").(string),
		"enabled":          d.Get("enabled").(bool),
	}
	
	// Update model aliases
	if d.HasChange("model_aliases") {
		if v, ok := d.GetOk("model_aliases"); ok {
			config["model_aliases"] = v.(map[string]interface{})
		}
	}
	
	// Update fallback chains
	if d.HasChange("fallback_chains") {
		if v, ok := d.GetOk("fallback_chains"); ok {
			fallbackMap := v.(map[string]interface{})
			processedFallbacks := make(map[string][]string)
			for model, fallbacks := range fallbackMap {
				fallbackStr := fallbacks.(string)
				fallbackList := []string{}
				for _, fb := range splitAndTrim(fallbackStr, ",") {
					if fb != "" {
						fallbackList = append(fallbackList, fb)
					}
				}
				processedFallbacks[model] = fallbackList
			}
			config["fallback_chains"] = processedFallbacks
		}
	}
	
	// Update complex configurations
	updateComplexConfig(d, "retry_config", config)
	updateComplexConfig(d, "timeout_config", config)
	updateComplexConfig(d, "load_balancing_config", config)
	updateComplexConfig(d, "cache_config", config)
	updateComplexConfig(d, "rate_limit_config", config)
	
	// Update the router configuration
	_, err := client.UpdateRouterConfig(d.Id(), config)
	if err != nil {
		return diag.Errorf("Error updating router configuration: %s", err)
	}
	
	log.Printf("[INFO] Updated router configuration with ID: %s", d.Id())
	
	return resourceLiteLLMRouterConfigRead(ctx, d, m)
}

func resourceLiteLLMRouterConfigDelete(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	client := m.(*Client)
	
	err := client.DeleteRouterConfig(d.Id())
	if err != nil {
		return diag.Errorf("Error deleting router configuration: %s", err)
	}
	
	log.Printf("[INFO] Deleted router configuration with ID: %s", d.Id())
	
	return nil
}

// Helper functions
func setComplexConfig(d *schema.ResourceData, key string, config map[string]interface{}) {
	if configData, ok := config[key].(map[string]interface{}); ok {
		d.Set(key, []interface{}{configData})
	}
}

func updateComplexConfig(d *schema.ResourceData, key string, config map[string]interface{}) {
	if d.HasChange(key) {
		if v, ok := d.GetOk(key); ok {
			list := v.([]interface{})
			if len(list) > 0 {
				config[key] = list[0].(map[string]interface{})
			}
		}
	}
}

// Client methods for router configuration management
func (c *Client) CreateRouterConfig(config map[string]interface{}) (map[string]interface{}, error) {
	body, _ := json.Marshal(config)
	return c.doRequest("POST", "/router/config/new", body)
}

func (c *Client) GetRouterConfig(routerID string) (map[string]interface{}, error) {
	return c.doRequest("GET", fmt.Sprintf("/router/config/%s", routerID), nil)
}

func (c *Client) UpdateRouterConfig(routerID string, config map[string]interface{}) (map[string]interface{}, error) {
	body, _ := json.Marshal(config)
	return c.doRequest("PUT", fmt.Sprintf("/router/config/%s", routerID), body)
}

func (c *Client) DeleteRouterConfig(routerID string) error {
	_, err := c.doRequest("DELETE", fmt.Sprintf("/router/config/%s", routerID), nil)
	return err
}