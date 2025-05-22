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
			"max_budget": {
				Type:     schema.TypeFloat,
				Optional: true,
			},
			"user_id": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"team_id": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"max_parallel_requests": {
				Type:     schema.TypeInt,
				Optional: true,
			},
			"metadata": {
				Type:     schema.TypeMap,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
			},
			"tpm_limit": {
				Type:     schema.TypeInt,
				Optional: true,
			},
			"rpm_limit": {
				Type:     schema.TypeInt,
				Optional: true,
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
				Elem:     &schema.Schema{Type: schema.TypeFloat},
			},
			"model_rpm_limit": {
				Type:     schema.TypeMap,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeInt},
			},
			"model_tpm_limit": {
				Type:     schema.TypeMap,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeInt},
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

	createdKey, err := c.CreateKey(key)
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
	if v, ok := d.GetOk("models"); ok {
		key.Models = expandStringList(v.([]interface{}))
	}
	if v, ok := d.GetOk("max_budget"); ok {
		key.MaxBudget = v.(float64)
	}
	if v, ok := d.GetOk("user_id"); ok {
		key.UserID = v.(string)
	}
	if v, ok := d.GetOk("team_id"); ok {
		key.TeamID = v.(string)
	}
	if v, ok := d.GetOk("max_parallel_requests"); ok {
		key.MaxParallelRequests = v.(int)
	}
	if v, ok := d.GetOk("metadata"); ok {
		key.Metadata = v.(map[string]interface{})
	}
	if v, ok := d.GetOk("tpm_limit"); ok {
		key.TPMLimit = v.(int)
	}
	if v, ok := d.GetOk("rpm_limit"); ok {
		key.RPMLimit = v.(int)
	}
	if v, ok := d.GetOk("budget_duration"); ok {
		key.BudgetDuration = v.(string)
	}
	if v, ok := d.GetOk("allowed_cache_controls"); ok {
		key.AllowedCacheControls = expandStringList(v.([]interface{}))
	}
	if v, ok := d.GetOk("soft_budget"); ok {
		key.SoftBudget = v.(float64)
	}
	if v, ok := d.GetOk("key_alias"); ok {
		key.KeyAlias = v.(string)
	}
	if v, ok := d.GetOk("duration"); ok {
		key.Duration = v.(string)
	}
	if v, ok := d.GetOk("aliases"); ok {
		key.Aliases = v.(map[string]interface{})
	}
	if v, ok := d.GetOk("config"); ok {
		key.Config = v.(map[string]interface{})
	}
	if v, ok := d.GetOk("permissions"); ok {
		key.Permissions = v.(map[string]interface{})
	}
	if v, ok := d.GetOk("model_max_budget"); ok {
		key.ModelMaxBudget = v.(map[string]interface{})
	}
	if v, ok := d.GetOk("model_rpm_limit"); ok {
		key.ModelRPMLimit = v.(map[string]interface{})
	}
	if v, ok := d.GetOk("model_tpm_limit"); ok {
		key.ModelTPMLimit = v.(map[string]interface{})
	}
	if v, ok := d.GetOk("guardrails"); ok {
		key.Guardrails = expandStringList(v.([]interface{}))
	}
	if v, ok := d.GetOk("blocked"); ok {
		key.Blocked = v.(bool)
	}
	if v, ok := d.GetOk("tags"); ok {
		key.Tags = expandStringList(v.([]interface{}))
	}
}

func mapKeyToResourceData(d *schema.ResourceData, key *Key) {
	d.Set("key", key.Key)

	if len(key.Models) > 0 {
		d.Set("models", key.Models)
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
