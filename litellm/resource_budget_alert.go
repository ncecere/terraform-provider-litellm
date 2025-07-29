package litellm

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
)

func resourceLiteLLMBudgetAlert() *schema.Resource {
	return &schema.Resource{
		CreateContext: resourceLiteLLMBudgetAlertCreate,
		ReadContext:   resourceLiteLLMBudgetAlertRead,
		UpdateContext: resourceLiteLLMBudgetAlertUpdate,
		DeleteContext: resourceLiteLLMBudgetAlertDelete,
		Importer: &schema.ResourceImporter{
			StateContext: schema.ImportStatePassthroughContext,
		},
		Description: "Manages budget alerts in LiteLLM for monitoring spending thresholds",
		Schema: map[string]*schema.Schema{
			"alert_name": {
				Type:        schema.TypeString,
				Required:    true,
				Description: "Name of the budget alert",
			},
			"threshold_percent": {
				Type:         schema.TypeFloat,
				Required:     true,
				ValidateFunc: validation.FloatBetween(0, 100),
				Description:  "Percentage of budget that triggers the alert (0-100)",
			},
			"budget_scope": {
				Type:         schema.TypeString,
				Required:     true,
				ValidateFunc: validation.StringInSlice([]string{"user", "team", "global", "key"}, false),
				Description:  "Scope of the budget alert (user, team, global, or key)",
			},
			"scope_id": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "ID of the user, team, or key (required for non-global scopes)",
			},
			"notification_channels": {
				Type:        schema.TypeList,
				Required:    true,
				Description: "List of notification channels (e.g., email:admin@company.com, webhook:https://...)",
				Elem: &schema.Schema{
					Type: schema.TypeString,
				},
			},
			"enabled": {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     true,
				Description: "Whether this alert is enabled",
			},
			"alert_frequency": {
				Type:         schema.TypeString,
				Optional:     true,
				Default:      "once",
				ValidateFunc: validation.StringInSlice([]string{"once", "daily", "weekly", "always"}, false),
				Description:  "How often to send alerts (once, daily, weekly, always)",
			},
			"include_usage_details": {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     true,
				Description: "Include detailed usage breakdown in alert notifications",
			},
			"metadata": {
				Type:        schema.TypeMap,
				Optional:    true,
				Description: "Additional metadata for the alert",
				Elem: &schema.Schema{
					Type: schema.TypeString,
				},
			},
		},
	}
}

func resourceLiteLLMBudgetAlertCreate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	client := m.(*Client)
	
	// Validate scope_id is provided for non-global scopes
	scope := d.Get("budget_scope").(string)
	if scope != "global" && d.Get("scope_id").(string) == "" {
		return diag.Errorf("scope_id is required for non-global budget scopes")
	}
	
	// Build the alert configuration
	alertConfig := map[string]interface{}{
		"alert_name":            d.Get("alert_name").(string),
		"threshold_percent":     d.Get("threshold_percent").(float64),
		"budget_scope":          scope,
		"enabled":               d.Get("enabled").(bool),
		"alert_frequency":       d.Get("alert_frequency").(string),
		"include_usage_details": d.Get("include_usage_details").(bool),
	}
	
	// Add scope_id for non-global scopes
	if scope != "global" {
		alertConfig["scope_id"] = d.Get("scope_id").(string)
	}
	
	// Convert notification channels to the expected format
	channels := d.Get("notification_channels").([]interface{})
	notificationConfig := make([]map[string]string, 0, len(channels))
	for _, channel := range channels {
		channelStr := channel.(string)
		parts := strings.SplitN(channelStr, ":", 2)
		if len(parts) != 2 {
			return diag.Errorf("Invalid notification channel format: %s (expected type:target)", channelStr)
		}
		notificationConfig = append(notificationConfig, map[string]string{
			"type":   parts[0],
			"target": parts[1],
		})
	}
	alertConfig["notification_channels"] = notificationConfig
	
	// Add metadata if provided
	if v, ok := d.GetOk("metadata"); ok {
		alertConfig["metadata"] = v.(map[string]interface{})
	}
	
	// Create the budget alert
	response, err := client.CreateBudgetAlert(alertConfig)
	if err != nil {
		return diag.Errorf("Error creating budget alert: %s", err)
	}
	
	// Extract the ID from the response
	if id, ok := response["alert_id"].(string); ok {
		d.SetId(id)
	} else {
		// Generate ID from scope and name
		d.SetId(fmt.Sprintf("%s-%s-%s", scope, d.Get("scope_id").(string), d.Get("alert_name").(string)))
	}
	
	log.Printf("[INFO] Created budget alert with ID: %s", d.Id())
	
	return resourceLiteLLMBudgetAlertRead(ctx, d, m)
}

func resourceLiteLLMBudgetAlertRead(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	client := m.(*Client)
	
	alert, err := client.GetBudgetAlert(d.Id())
	if err != nil {
		if err.Error() == "budget alert not found" {
			log.Printf("[WARN] Budget alert %s not found, removing from state", d.Id())
			d.SetId("")
			return nil
		}
		return diag.Errorf("Error reading budget alert: %s", err)
	}
	
	// Set the attributes
	d.Set("alert_name", alert["alert_name"])
	d.Set("threshold_percent", alert["threshold_percent"])
	d.Set("budget_scope", alert["budget_scope"])
	d.Set("scope_id", alert["scope_id"])
	d.Set("enabled", alert["enabled"])
	d.Set("alert_frequency", alert["alert_frequency"])
	d.Set("include_usage_details", alert["include_usage_details"])
	
	// Convert notification channels back to string format
	if channels, ok := alert["notification_channels"].([]interface{}); ok {
		channelStrings := make([]string, 0, len(channels))
		for _, channel := range channels {
			if ch, ok := channel.(map[string]interface{}); ok {
				channelStrings = append(channelStrings, fmt.Sprintf("%s:%s", ch["type"], ch["target"]))
			}
		}
		d.Set("notification_channels", channelStrings)
	}
	
	if metadata, ok := alert["metadata"].(map[string]interface{}); ok {
		d.Set("metadata", metadata)
	}
	
	return nil
}

func resourceLiteLLMBudgetAlertUpdate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	client := m.(*Client)
	
	// Build the updated alert configuration
	alertConfig := map[string]interface{}{
		"alert_name":            d.Get("alert_name").(string),
		"threshold_percent":     d.Get("threshold_percent").(float64),
		"enabled":               d.Get("enabled").(bool),
		"alert_frequency":       d.Get("alert_frequency").(string),
		"include_usage_details": d.Get("include_usage_details").(bool),
	}
	
	// Convert notification channels
	channels := d.Get("notification_channels").([]interface{})
	notificationConfig := make([]map[string]string, 0, len(channels))
	for _, channel := range channels {
		channelStr := channel.(string)
		parts := strings.SplitN(channelStr, ":", 2)
		if len(parts) != 2 {
			return diag.Errorf("Invalid notification channel format: %s", channelStr)
		}
		notificationConfig = append(notificationConfig, map[string]string{
			"type":   parts[0],
			"target": parts[1],
		})
	}
	alertConfig["notification_channels"] = notificationConfig
	
	// Add metadata if provided
	if v, ok := d.GetOk("metadata"); ok {
		alertConfig["metadata"] = v.(map[string]interface{})
	}
	
	// Update the budget alert
	_, err := client.UpdateBudgetAlert(d.Id(), alertConfig)
	if err != nil {
		return diag.Errorf("Error updating budget alert: %s", err)
	}
	
	log.Printf("[INFO] Updated budget alert with ID: %s", d.Id())
	
	return resourceLiteLLMBudgetAlertRead(ctx, d, m)
}

func resourceLiteLLMBudgetAlertDelete(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	client := m.(*Client)
	
	err := client.DeleteBudgetAlert(d.Id())
	if err != nil {
		return diag.Errorf("Error deleting budget alert: %s", err)
	}
	
	log.Printf("[INFO] Deleted budget alert with ID: %s", d.Id())
	
	return nil
}

// Client methods for budget alert management
func (c *Client) CreateBudgetAlert(config map[string]interface{}) (map[string]interface{}, error) {
	body, _ := json.Marshal(config)
	return c.doRequest("POST", "/budget/alert/new", body)
}

func (c *Client) GetBudgetAlert(alertID string) (map[string]interface{}, error) {
	return c.doRequest("GET", fmt.Sprintf("/budget/alert/%s", alertID), nil)
}

func (c *Client) UpdateBudgetAlert(alertID string, config map[string]interface{}) (map[string]interface{}, error) {
	body, _ := json.Marshal(config)
	return c.doRequest("PUT", fmt.Sprintf("/budget/alert/%s", alertID), body)
}

func (c *Client) DeleteBudgetAlert(alertID string) error {
	_, err := c.doRequest("DELETE", fmt.Sprintf("/budget/alert/%s", alertID), nil)
	return err
}