package litellm

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func resourceKey() *schema.Resource {
	return &schema.Resource{
		CreateContext: resourceKeyCreate,
		ReadContext:   resourceKeyRead,
		UpdateContext: resourceKeyUpdate,
		DeleteContext: resourceKeyDelete,
		Schema: map[string]*schema.Schema{
			"key": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"models": {
				Type:     schema.TypeList,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
			},
			"allowed_routes": {
				Type:     schema.TypeList,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
			},
			"allowed_passthrough_routes": {
				Type:     schema.TypeList,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
			},
			"max_budget": {
				Type:     schema.TypeFloat,
				Optional: true,
				Computed: true,
			},
			"user_id": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"team_id": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"service_account_id": {
				Type:        schema.TypeString,
				Optional:    true,
				ForceNew:    true,
				Description: "Create a team-owned service account key using this identifier",
			},
			"max_parallel_requests": {
				Type:     schema.TypeInt,
				Optional: true,
				Computed: true,
			},
			"metadata": {
				Type:     schema.TypeMap,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
			},
			"tpm_limit": {
				Type:     schema.TypeInt,
				Optional: true,
				Computed: true,
			},
			"rpm_limit": {
				Type:     schema.TypeInt,
				Optional: true,
				Computed: true,
			},
			"budget_duration": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"allowed_cache_controls": {
				Type:     schema.TypeList,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
			},
			"soft_budget": {
				Type:     schema.TypeFloat,
				Optional: true,
				Computed: true,
			},
			"key_alias": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"duration": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"aliases": {
				Type:     schema.TypeMap,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
			},
			"config": {
				Type:     schema.TypeMap,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
			},
			"permissions": {
				Type:     schema.TypeMap,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
			},
			"model_max_budget": {
				Type:     schema.TypeMap,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeFloat, Computed: true},
			},
			"model_rpm_limit": {
				Type:     schema.TypeMap,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeInt, Computed: true},
			},
			"model_tpm_limit": {
				Type:     schema.TypeMap,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeInt, Computed: true},
			},
			"guardrails": {
				Type:     schema.TypeList,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
			},
			"blocked": {
				Type:     schema.TypeBool,
				Optional: true,
			},
			"tags": {
				Type:     schema.TypeList,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
			},
			"spend": {
				Type:     schema.TypeFloat,
				Computed: true,
			},
		},
	}
}

func resourceKeyCreate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	c := m.(*Client)

	key := &Key{}
	mapResourceDataToKey(d, key)

	var (
		createdKey *Key
		err        error
	)
	if d.Get("service_account_id").(string) != "" {
		createdKey, err = c.CreateServiceAccountKey(key)
	} else {
		createdKey, err = c.CreateKey(key)
	}
	if err != nil {
		return diag.FromErr(fmt.Errorf("error creating key: %s", err))
	}

	d.SetId(createdKey.Key)
	return resourceKeyRead(ctx, d, m)
}

func resourceKeyRead(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	c := m.(*Client)

	key, err := c.GetKey(d.Id())
	if err != nil {
		return diag.FromErr(fmt.Errorf("error reading key: %s", err))
	}

	if key == nil {
		d.SetId("")
		return nil
	}

	mapKeyToResourceData(d, key)
	return nil
}

func resourceKeyUpdate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	c := m.(*Client)

	key := &Key{Key: d.Id()}
	mapResourceDataToKey(d, key)

	_, err := c.UpdateKey(key)
	if err != nil {
		return diag.FromErr(fmt.Errorf("error updating key: %s", err))
	}

	return resourceKeyRead(ctx, d, m)
}

func resourceKeyDelete(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	c := m.(*Client)

	err := c.DeleteKey(d.Id())
	if err != nil {
		return diag.FromErr(fmt.Errorf("error deleting key: %s", err))
	}

	d.SetId("")
	return nil
}

func mapResourceDataToKey(d *schema.ResourceData, key *Key) {
	teamID := d.Get("team_id").(string)
	key.TeamID = teamID
	if modelsRaw, ok := d.GetOkExists("models"); ok {
		key.Models = expandStringList(modelsRaw.([]interface{}))
		if len(key.Models) == 0 && teamID != "" {
			key.Models = []string{"all-team-models"}
		}
	} else if teamID != "" {
		key.Models = []string{"all-team-models"}
	}

	if routesRaw, ok := d.GetOkExists("allowed_routes"); ok {
		key.AllowedRoutes = expandStringList(routesRaw.([]interface{}))
	}
	if routesRaw, ok := d.GetOkExists("allowed_passthrough_routes"); ok {
		key.AllowedPassthroughRoutes = expandStringList(routesRaw.([]interface{}))
	}

	key.MaxBudget = d.Get("max_budget").(float64)
	key.UserID = d.Get("user_id").(string)
	key.MaxParallelRequests = d.Get("max_parallel_requests").(int)
	key.Metadata = d.Get("metadata").(map[string]interface{})
	key.TPMLimit = d.Get("tpm_limit").(int)
	key.RPMLimit = d.Get("rpm_limit").(int)
	key.BudgetDuration = d.Get("budget_duration").(string)
	key.AllowedCacheControls = expandStringList(d.Get("allowed_cache_controls").([]interface{}))
	key.SoftBudget = d.Get("soft_budget").(float64)
	key.KeyAlias = d.Get("key_alias").(string)
	key.Duration = d.Get("duration").(string)
	key.Aliases = d.Get("aliases").(map[string]interface{})
	key.Config = d.Get("config").(map[string]interface{})
	key.Permissions = d.Get("permissions").(map[string]interface{})
	key.ModelMaxBudget = d.Get("model_max_budget").(map[string]interface{})
	key.ModelRPMLimit = d.Get("model_rpm_limit").(map[string]interface{})
	key.ModelTPMLimit = d.Get("model_tpm_limit").(map[string]interface{})
	key.Guardrails = expandStringList(d.Get("guardrails").([]interface{}))
	key.Blocked = d.Get("blocked").(bool)
	key.Tags = expandStringList(d.Get("tags").([]interface{}))

	applyServiceAccountSettings(d, key)
}

func mapKeyToResourceData(d *schema.ResourceData, key *Key) {
	d.Set("key", key.Key)

	if len(key.Models) > 0 {
		d.Set("models", key.Models)
	}
	if len(key.AllowedRoutes) > 0 {
		d.Set("allowed_routes", key.AllowedRoutes)
	}
	if len(key.AllowedPassthroughRoutes) > 0 {
		d.Set("allowed_passthrough_routes", key.AllowedPassthroughRoutes)
	}
	if key.MaxBudget != 0 {
		d.Set("max_budget", key.MaxBudget)
	}
	if key.UserID != "" {
		d.Set("user_id", key.UserID)
	}
	if key.TeamID != "" {
		d.Set("team_id", key.TeamID)
	}
	if key.MaxParallelRequests != 0 {
		d.Set("max_parallel_requests", key.MaxParallelRequests)
	}
	if key.Metadata != nil {
		d.Set("metadata", key.Metadata)
	}
	if key.TPMLimit != 0 {
		d.Set("tpm_limit", key.TPMLimit)
	}
	if key.RPMLimit != 0 {
		d.Set("rpm_limit", key.RPMLimit)
	}
	if key.BudgetDuration != "" {
		d.Set("budget_duration", key.BudgetDuration)
	}
	if len(key.AllowedCacheControls) > 0 {
		d.Set("allowed_cache_controls", key.AllowedCacheControls)
	}
	if key.SoftBudget != 0 {
		d.Set("soft_budget", key.SoftBudget)
	}
	if key.KeyAlias != "" {
		d.Set("key_alias", key.KeyAlias)
	}
	if key.Duration != "" {
		d.Set("duration", key.Duration)
	}
	if key.Aliases != nil {
		d.Set("aliases", key.Aliases)
	}
	if key.Config != nil {
		d.Set("config", key.Config)
	}
	if key.Permissions != nil {
		d.Set("permissions", key.Permissions)
	}
	if key.ModelMaxBudget != nil {
		d.Set("model_max_budget", key.ModelMaxBudget)
	}
	if key.ModelRPMLimit != nil {
		d.Set("model_rpm_limit", key.ModelRPMLimit)
	}
	if key.ModelTPMLimit != nil {
		d.Set("model_tpm_limit", key.ModelTPMLimit)
	}
	if len(key.Guardrails) > 0 {
		d.Set("guardrails", key.Guardrails)
	}
	d.Set("blocked", key.Blocked)
	if len(key.Tags) > 0 {
		d.Set("tags", key.Tags)
	}
	if key.Spend != 0 {
		d.Set("spend", key.Spend)
	}
}

func applyServiceAccountSettings(d *schema.ResourceData, key *Key) {
	serviceAccountID := d.Get("service_account_id").(string)
	if serviceAccountID == "" {
		return
	}

	if key.Metadata == nil {
		key.Metadata = make(map[string]interface{})
	}
	if _, exists := key.Metadata["service_account_id"]; !exists {
		key.Metadata["service_account_id"] = serviceAccountID
	}
	if key.KeyAlias == "" {
		key.KeyAlias = serviceAccountID
	}
}
