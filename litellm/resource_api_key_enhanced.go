package litellm

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
)

func resourceLiteLLMAPIKeyEnhanced() *schema.Resource {
	return &schema.Resource{
		CreateContext: resourceLiteLLMAPIKeyEnhancedCreate,
		ReadContext:   resourceLiteLLMAPIKeyEnhancedRead,
		UpdateContext: resourceLiteLLMAPIKeyEnhancedUpdate,
		DeleteContext: resourceLiteLLMAPIKeyEnhancedDelete,
		Importer: &schema.ResourceImporter{
			StateContext: func(ctx context.Context, d *schema.ResourceData, m interface{}) ([]*schema.ResourceData, error) {
				// Import requires the key hash
				d.Set("import_hash", d.Id())
				return []*schema.ResourceData{d}, nil
			},
		},
		Description: "Enhanced API key management in LiteLLM with advanced permissions and lifecycle management",
		Schema: map[string]*schema.Schema{
			"key_alias": {
				Type:        schema.TypeString,
				Required:    true,
				Description: "Alias for the API key",
			},
			"user_id": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "User ID associated with this key",
			},
			"team_id": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "Team ID associated with this key",
			},
			"permissions": {
				Type:        schema.TypeList,
				Optional:    true,
				MaxItems:    1,
				Description: "Permissions and restrictions for the API key",
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"models": {
							Type:        schema.TypeSet,
							Optional:    true,
							Description: "List of allowed models",
							Elem: &schema.Schema{
								Type: schema.TypeString,
							},
						},
						"max_budget": {
							Type:         schema.TypeFloat,
							Optional:     true,
							Default:      0,
							ValidateFunc: validation.FloatAtLeast(0),
							Description:  "Maximum budget for this key (0 = unlimited)",
						},
						"budget_duration": {
							Type:         schema.TypeString,
							Optional:     true,
							Default:      "1mo",
							ValidateFunc: validation.StringInSlice([]string{"1d", "1w", "1mo", "1y"}, false),
							Description:  "Budget duration (1d, 1w, 1mo, 1y)",
						},
						"max_parallel_requests": {
							Type:         schema.TypeInt,
							Optional:     true,
							Default:      100,
							ValidateFunc: validation.IntBetween(1, 10000),
							Description:  "Maximum parallel requests allowed",
						},
						"allowed_endpoints": {
							Type:        schema.TypeSet,
							Optional:    true,
							Description: "List of allowed API endpoints",
							Elem: &schema.Schema{
								Type: schema.TypeString,
							},
						},
						"metadata_filters": {
							Type:        schema.TypeMap,
							Optional:    true,
							Description: "Metadata filters for request validation",
							Elem: &schema.Schema{
								Type: schema.TypeString,
							},
						},
					},
				},
			},
			"expires_at": {
				Type:         schema.TypeString,
				Optional:     true,
				ValidateFunc: validation.IsRFC3339Time,
				Description:  "Expiration time for the key (RFC3339 format)",
			},
			"soft_budget_limit": {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
				Description: "If true, allows requests to exceed budget with warnings",
			},
			"auto_renew": {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
				Description: "Automatically renew key before expiration",
			},
			"renewal_period_days": {
				Type:         schema.TypeInt,
				Optional:     true,
				Default:      7,
				ValidateFunc: validation.IntBetween(1, 30),
				Description:  "Days before expiration to auto-renew",
			},
			"tags": {
				Type:        schema.TypeMap,
				Optional:    true,
				Description: "Tags for organizing and filtering keys",
				Elem: &schema.Schema{
					Type: schema.TypeString,
				},
			},
			"notification_config": {
				Type:        schema.TypeList,
				Optional:    true,
				MaxItems:    1,
				Description: "Notification configuration for key events",
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"budget_alerts": {
							Type:        schema.TypeSet,
							Optional:    true,
							Description: "Budget thresholds for alerts (percentages)",
							Elem: &schema.Schema{
								Type:         schema.TypeInt,
								ValidateFunc: validation.IntBetween(1, 100),
							},
						},
						"expiry_alert_days": {
							Type:         schema.TypeInt,
							Optional:     true,
							Default:      7,
							ValidateFunc: validation.IntBetween(1, 30),
							Description:  "Days before expiry to send alert",
						},
						"webhook_url": {
							Type:         schema.TypeString,
							Optional:     true,
							ValidateFunc: validation.IsURLWithHTTPorHTTPS,
							Description:  "Webhook URL for notifications",
						},
					},
				},
			},
			"rate_limits": {
				Type:        schema.TypeList,
				Optional:    true,
				MaxItems:    1,
				Description: "Rate limiting configuration",
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"requests_per_minute": {
							Type:         schema.TypeInt,
							Optional:     true,
							ValidateFunc: validation.IntBetween(1, 100000),
							Description:  "Requests per minute limit",
						},
						"tokens_per_minute": {
							Type:         schema.TypeInt,
							Optional:     true,
							ValidateFunc: validation.IntBetween(1, 10000000),
							Description:  "Tokens per minute limit",
						},
						"requests_per_day": {
							Type:         schema.TypeInt,
							Optional:     true,
							ValidateFunc: validation.IntBetween(1, 10000000),
							Description:  "Requests per day limit",
						},
					},
				},
			},
			// Computed attributes
			"key": {
				Type:        schema.TypeString,
				Computed:    true,
				Sensitive:   true,
				Description: "The actual API key (only available on create)",
			},
			"key_hash": {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "Hash of the API key for identification",
			},
			"created_at": {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "Creation timestamp",
			},
			"last_used_at": {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "Last usage timestamp",
			},
			"total_spend": {
				Type:        schema.TypeFloat,
				Computed:    true,
				Description: "Total spend on this key",
			},
			"import_hash": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "Key hash for importing existing keys",
			},
		},
	}
}

func resourceLiteLLMAPIKeyEnhancedCreate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	client := m.(*Client)
	
	// Build the API key configuration
	keyConfig := map[string]interface{}{
		"key_alias":         d.Get("key_alias").(string),
		"soft_budget_limit": d.Get("soft_budget_limit").(bool),
		"auto_renew":        d.Get("auto_renew").(bool),
		"renewal_period_days": d.Get("renewal_period_days").(int),
	}
	
	// Add optional fields
	if v, ok := d.GetOk("user_id"); ok {
		keyConfig["user_id"] = v.(string)
	}
	
	if v, ok := d.GetOk("team_id"); ok {
		keyConfig["team_id"] = v.(string)
	}
	
	if v, ok := d.GetOk("expires_at"); ok {
		keyConfig["expires_at"] = v.(string)
	}
	
	// Process permissions
	if v, ok := d.GetOk("permissions"); ok {
		permList := v.([]interface{})
		if len(permList) > 0 {
			perms := permList[0].(map[string]interface{})
			permissions := map[string]interface{}{
				"max_budget":            perms["max_budget"],
				"budget_duration":       perms["budget_duration"],
				"max_parallel_requests": perms["max_parallel_requests"],
			}
			
			if models, ok := perms["models"].(*schema.Set); ok {
				permissions["models"] = models.List()
			}
			
			if endpoints, ok := perms["allowed_endpoints"].(*schema.Set); ok {
				permissions["allowed_endpoints"] = endpoints.List()
			}
			
			if filters, ok := perms["metadata_filters"].(map[string]interface{}); ok {
				permissions["metadata_filters"] = filters
			}
			
			keyConfig["permissions"] = permissions
		}
	}
	
	// Process tags
	if v, ok := d.GetOk("tags"); ok {
		keyConfig["tags"] = v.(map[string]interface{})
	}
	
	// Process notification config
	if v, ok := d.GetOk("notification_config"); ok {
		notifList := v.([]interface{})
		if len(notifList) > 0 {
			notif := notifList[0].(map[string]interface{})
			notifConfig := map[string]interface{}{
				"expiry_alert_days": notif["expiry_alert_days"],
			}
			
			if alerts, ok := notif["budget_alerts"].(*schema.Set); ok {
				notifConfig["budget_alerts"] = alerts.List()
			}
			
			if webhook, ok := notif["webhook_url"].(string); ok && webhook != "" {
				notifConfig["webhook_url"] = webhook
			}
			
			keyConfig["notification_config"] = notifConfig
		}
	}
	
	// Process rate limits
	if v, ok := d.GetOk("rate_limits"); ok {
		rateList := v.([]interface{})
		if len(rateList) > 0 {
			keyConfig["rate_limits"] = rateList[0].(map[string]interface{})
		}
	}
	
	// Create the API key
	response, err := client.CreateAPIKeyEnhanced(keyConfig)
	if err != nil {
		return diag.Errorf("Error creating enhanced API key: %s", err)
	}
	
	// Extract key information
	if keyHash, ok := response["key_hash"].(string); ok {
		d.SetId(keyHash)
		d.Set("key_hash", keyHash)
	}
	
	if key, ok := response["key"].(string); ok {
		d.Set("key", key)
	}
	
	if createdAt, ok := response["created_at"].(string); ok {
		d.Set("created_at", createdAt)
	}
	
	log.Printf("[INFO] Created enhanced API key with hash: %s", d.Id())
	
	return resourceLiteLLMAPIKeyEnhancedRead(ctx, d, m)
}

func resourceLiteLLMAPIKeyEnhancedRead(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	client := m.(*Client)
	
	// Use import_hash if available (for imports)
	keyHash := d.Id()
	if importHash, ok := d.GetOk("import_hash"); ok {
		keyHash = importHash.(string)
		d.SetId(keyHash)
		d.Set("key_hash", keyHash)
		d.Set("import_hash", "") // Clear after use
	}
	
	keyInfo, err := client.GetAPIKeyEnhanced(keyHash)
	if err != nil {
		if err.Error() == "api key not found" {
			log.Printf("[WARN] Enhanced API key %s not found, removing from state", keyHash)
			d.SetId("")
			return nil
		}
		return diag.Errorf("Error reading enhanced API key: %s", err)
	}
	
	// Set basic attributes
	d.Set("key_alias", keyInfo["key_alias"])
	d.Set("user_id", keyInfo["user_id"])
	d.Set("team_id", keyInfo["team_id"])
	d.Set("expires_at", keyInfo["expires_at"])
	d.Set("soft_budget_limit", keyInfo["soft_budget_limit"])
	d.Set("auto_renew", keyInfo["auto_renew"])
	d.Set("renewal_period_days", keyInfo["renewal_period_days"])
	d.Set("created_at", keyInfo["created_at"])
	d.Set("last_used_at", keyInfo["last_used_at"])
	d.Set("total_spend", keyInfo["total_spend"])
	
	// Set permissions
	if perms, ok := keyInfo["permissions"].(map[string]interface{}); ok {
		permissions := []interface{}{
			map[string]interface{}{
				"max_budget":            perms["max_budget"],
				"budget_duration":       perms["budget_duration"],
				"max_parallel_requests": perms["max_parallel_requests"],
				"models":                perms["models"],
				"allowed_endpoints":     perms["allowed_endpoints"],
				"metadata_filters":      perms["metadata_filters"],
			},
		}
		d.Set("permissions", permissions)
	}
	
	// Set tags
	if tags, ok := keyInfo["tags"].(map[string]interface{}); ok {
		d.Set("tags", tags)
	}
	
	// Set notification config
	if notif, ok := keyInfo["notification_config"].(map[string]interface{}); ok {
		notifConfig := []interface{}{
			map[string]interface{}{
				"budget_alerts":     notif["budget_alerts"],
				"expiry_alert_days": notif["expiry_alert_days"],
				"webhook_url":       notif["webhook_url"],
			},
		}
		d.Set("notification_config", notifConfig)
	}
	
	// Set rate limits
	if rates, ok := keyInfo["rate_limits"].(map[string]interface{}); ok {
		rateLimits := []interface{}{rates}
		d.Set("rate_limits", rateLimits)
	}
	
	return nil
}

func resourceLiteLLMAPIKeyEnhancedUpdate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	client := m.(*Client)
	
	// Build update configuration
	updateConfig := map[string]interface{}{
		"key_alias":           d.Get("key_alias").(string),
		"soft_budget_limit":   d.Get("soft_budget_limit").(bool),
		"auto_renew":          d.Get("auto_renew").(bool),
		"renewal_period_days": d.Get("renewal_period_days").(int),
	}
	
	// Update permissions if changed
	if d.HasChange("permissions") {
		if v, ok := d.GetOk("permissions"); ok {
			permList := v.([]interface{})
			if len(permList) > 0 {
				perms := permList[0].(map[string]interface{})
				permissions := map[string]interface{}{
					"max_budget":            perms["max_budget"],
					"budget_duration":       perms["budget_duration"],
					"max_parallel_requests": perms["max_parallel_requests"],
				}
				
				if models, ok := perms["models"].(*schema.Set); ok {
					permissions["models"] = models.List()
				}
				
				if endpoints, ok := perms["allowed_endpoints"].(*schema.Set); ok {
					permissions["allowed_endpoints"] = endpoints.List()
				}
				
				if filters, ok := perms["metadata_filters"].(map[string]interface{}); ok {
					permissions["metadata_filters"] = filters
				}
				
				updateConfig["permissions"] = permissions
			}
		}
	}
	
	// Update other fields if changed
	if d.HasChange("expires_at") {
		updateConfig["expires_at"] = d.Get("expires_at").(string)
	}
	
	if d.HasChange("tags") {
		updateConfig["tags"] = d.Get("tags").(map[string]interface{})
	}
	
	if d.HasChange("notification_config") {
		if v, ok := d.GetOk("notification_config"); ok {
			notifList := v.([]interface{})
			if len(notifList) > 0 {
				updateConfig["notification_config"] = notifList[0].(map[string]interface{})
			}
		}
	}
	
	if d.HasChange("rate_limits") {
		if v, ok := d.GetOk("rate_limits"); ok {
			rateList := v.([]interface{})
			if len(rateList) > 0 {
				updateConfig["rate_limits"] = rateList[0].(map[string]interface{})
			}
		}
	}
	
	// Update the API key
	_, err := client.UpdateAPIKeyEnhanced(d.Id(), updateConfig)
	if err != nil {
		return diag.Errorf("Error updating enhanced API key: %s", err)
	}
	
	log.Printf("[INFO] Updated enhanced API key with hash: %s", d.Id())
	
	return resourceLiteLLMAPIKeyEnhancedRead(ctx, d, m)
}

func resourceLiteLLMAPIKeyEnhancedDelete(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	client := m.(*Client)
	
	err := client.DeleteAPIKeyEnhanced(d.Id())
	if err != nil {
		return diag.Errorf("Error deleting enhanced API key: %s", err)
	}
	
	log.Printf("[INFO] Deleted enhanced API key with hash: %s", d.Id())
	
	return nil
}

// Client methods for enhanced API key management
func (c *Client) CreateAPIKeyEnhanced(config map[string]interface{}) (map[string]interface{}, error) {
	body, _ := json.Marshal(config)
	return c.doRequest("POST", "/key/enhanced/new", body)
}

func (c *Client) GetAPIKeyEnhanced(keyHash string) (map[string]interface{}, error) {
	return c.doRequest("GET", fmt.Sprintf("/key/enhanced/%s", keyHash), nil)
}

func (c *Client) UpdateAPIKeyEnhanced(keyHash string, config map[string]interface{}) (map[string]interface{}, error) {
	body, _ := json.Marshal(config)
	return c.doRequest("PUT", fmt.Sprintf("/key/enhanced/%s", keyHash), body)
}

func (c *Client) DeleteAPIKeyEnhanced(keyHash string) error {
	_, err := c.doRequest("DELETE", fmt.Sprintf("/key/enhanced/%s", keyHash), nil)
	return err
}