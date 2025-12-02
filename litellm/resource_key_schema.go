package litellm

import (
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func resourceKeySchema() map[string]*schema.Schema {
	return map[string]*schema.Schema{
		"key": {
			Type:      schema.TypeString,
			Computed:  true,
			Sensitive: true,
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
		"spend": {
			Type:     schema.TypeFloat,
			Computed: true,
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
		"send_invite_email": {
			Type:     schema.TypeBool,
			Optional: true,
		},
	}
}
