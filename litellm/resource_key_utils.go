package litellm

import (
	"fmt"
	"log"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func buildKeyData(d *schema.ResourceData) map[string]interface{} {
	keyData := make(map[string]interface{})

	if v, ok := d.GetOkExists("models"); ok {
		keyData["models"] = expandStringList(v.([]interface{}))
	}
	if v, ok := d.GetOkExists("max_budget"); ok {
		keyData["max_budget"] = v.(float64)
	}
	if v, ok := d.GetOkExists("user_id"); ok {
		keyData["user_id"] = v.(string)
	}
	if v, ok := d.GetOkExists("team_id"); ok {
		keyData["team_id"] = v.(string)
	}
	if v, ok := d.GetOkExists("max_parallel_requests"); ok {
		keyData["max_parallel_requests"] = v.(int)
	}
	if v, ok := d.GetOkExists("metadata"); ok {
		keyData["metadata"] = v.(map[string]interface{})
	}
	if v, ok := d.GetOkExists("tpm_limit"); ok {
		keyData["tpm_limit"] = v.(int)
	}
	if v, ok := d.GetOkExists("rpm_limit"); ok {
		keyData["rpm_limit"] = v.(int)
	}
	if v, ok := d.GetOkExists("budget_duration"); ok {
		keyData["budget_duration"] = v.(string)
	}
	if v, ok := d.GetOkExists("allowed_cache_controls"); ok {
		keyData["allowed_cache_controls"] = expandStringList(v.([]interface{}))
	}
	if v, ok := d.GetOkExists("soft_budget"); ok {
		keyData["soft_budget"] = v.(float64)
	}
	if v, ok := d.GetOkExists("key_alias"); ok {
		keyData["key_alias"] = v.(string)
	}
	if v, ok := d.GetOkExists("duration"); ok {
		keyData["duration"] = v.(string)
	}
	if v, ok := d.GetOkExists("aliases"); ok {
		keyData["aliases"] = v.(map[string]interface{})
	}
	if v, ok := d.GetOkExists("config"); ok {
		keyData["config"] = v.(map[string]interface{})
	}
	if v, ok := d.GetOkExists("permissions"); ok {
		keyData["permissions"] = v.(map[string]interface{})
	}
	if v, ok := d.GetOkExists("model_max_budget"); ok {
		keyData["model_max_budget"] = v.(map[string]interface{})
	}
	if v, ok := d.GetOkExists("model_rpm_limit"); ok {
		keyData["model_rpm_limit"] = v.(map[string]interface{})
	}
	if v, ok := d.GetOkExists("model_tpm_limit"); ok {
		keyData["model_tpm_limit"] = v.(map[string]interface{})
	}
	if v, ok := d.GetOkExists("guardrails"); ok {
		keyData["guardrails"] = expandStringList(v.([]interface{}))
	}
	if v, ok := d.GetOkExists("tags"); ok {
		keyData["tags"] = expandStringList(v.([]interface{}))
	}

	return keyData
}

func setKeyResourceData(d *schema.ResourceData, key *Key) error {
	fields := map[string]interface{}{
		"key":                    key.Key,
		"models":                 key.Models,
		"spend":                  key.Spend,
		"max_budget":             key.MaxBudget,
		"user_id":                key.UserID,
		"team_id":                key.TeamID,
		"max_parallel_requests":  key.MaxParallelRequests,
		"metadata":               key.Metadata,
		"tpm_limit":              key.TPMLimit,
		"rpm_limit":              key.RPMLimit,
		"budget_duration":        key.BudgetDuration,
		"allowed_cache_controls": key.AllowedCacheControls,
		"soft_budget":            key.SoftBudget,
		"key_alias":              key.KeyAlias,
		"duration":               key.Duration,
		"aliases":                key.Aliases,
		"config":                 key.Config,
		"permissions":            key.Permissions,
		"model_max_budget":       key.ModelMaxBudget,
		"model_rpm_limit":        key.ModelRPMLimit,
		"model_tpm_limit":        key.ModelTPMLimit,
		"guardrails":             key.Guardrails,
		"tags":                   key.Tags,
	}

	for field, value := range fields {
		if err := d.Set(field, value); err != nil {
			log.Printf("[WARN] Error setting %s: %s", field, err)
			return fmt.Errorf("error setting %s: %s", field, err)
		}
	}

	return nil
}

func expandStringList(list []interface{}) []string {
	result := make([]string, len(list))
	for i, v := range list {
		result[i] = v.(string)
	}
	return result
}

func mapToKey(data map[string]interface{}) *Key {
	key := &Key{}
	for k, v := range data {
		switch k {
		case "key":
			key.Key = v.(string)
		case "models":
			key.Models = v.([]string)
		case "max_budget":
			key.MaxBudget = v.(float64)
		case "user_id":
			key.UserID = v.(string)
		case "team_id":
			key.TeamID = v.(string)
		case "max_parallel_requests":
			key.MaxParallelRequests = v.(int)
		case "metadata":
			key.Metadata = v.(map[string]interface{})
		case "tpm_limit":
			key.TPMLimit = v.(int)
		case "rpm_limit":
			key.RPMLimit = v.(int)
		case "budget_duration":
			key.BudgetDuration = v.(string)
		case "allowed_cache_controls":
			key.AllowedCacheControls = v.([]string)
		case "soft_budget":
			key.SoftBudget = v.(float64)
		case "key_alias":
			key.KeyAlias = v.(string)
		case "duration":
			key.Duration = v.(string)
		case "aliases":
			key.Aliases = v.(map[string]interface{})
		case "config":
			key.Config = v.(map[string]interface{})
		case "permissions":
			key.Permissions = v.(map[string]interface{})
		case "model_max_budget":
			key.ModelMaxBudget = v.(map[string]interface{})
		case "model_rpm_limit":
			key.ModelRPMLimit = v.(map[string]interface{})
		case "model_tpm_limit":
			key.ModelTPMLimit = v.(map[string]interface{})
		case "guardrails":
			key.Guardrails = v.([]string)
		case "tags":
			key.Tags = v.([]string)
		}
	}
	return key
}

func buildKeyForCreation(data map[string]interface{}) *Key {
	return mapToKey(data)
}
