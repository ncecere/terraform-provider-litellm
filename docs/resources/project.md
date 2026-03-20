# litellm_project Resource

Manages a LiteLLM Project. Projects sit between teams and keys in the hierarchy, allowing fine-grained budget and model access control within a team.

## Example Usage

### Minimal Project

```hcl
resource "litellm_project" "simple" {
  team_id = litellm_team.example.id
}
```

### Full Project

```hcl
resource "litellm_project" "full" {
  project_alias = "production-api"
  description   = "Production API project for the platform team"
  team_id       = litellm_team.platform.id

  models     = ["gpt-4o", "gpt-4o-mini", "claude-sonnet-4-20250514"]
  tags       = ["production", "platform"]
  max_budget = 1000.0
  soft_budget = 800.0
  budget_duration = "30d"

  tpm_limit = 100000
  rpm_limit = 1000

  metadata = {
    environment = "production"
    cost_center = "engineering"
  }

  model_rpm_limit = {
    "gpt-4o"     = 500
    "gpt-4o-mini" = 1000
  }

  model_tpm_limit = {
    "gpt-4o"     = 50000
    "gpt-4o-mini" = 100000
  }
}
```

## Argument Reference

* `team_id` - (Required, ForceNew) The team ID that this project belongs to. Changing this forces a new resource.
* `project_alias` - (Optional) Human-friendly name for the project.
* `description` - (Optional) Description of the project's purpose and use case.
* `models` - (Optional) List of models the project can access.
* `metadata` - (Optional) Metadata for the project. Values are strings; use `jsonencode()` for complex values.
* `tags` - (Optional) Tags associated with the project.
* `max_budget` - (Optional) Maximum budget for this project.
* `soft_budget` - (Optional) Soft budget limit for warnings.
* `budget_duration` - (Optional) Budget reset duration (e.g. `30d`, `1h`).
* `budget_id` - (Optional) Budget ID to associate with this project.
* `tpm_limit` - (Optional) Tokens per minute limit.
* `rpm_limit` - (Optional) Requests per minute limit.
* `max_parallel_requests` - (Optional) Maximum parallel requests allowed.
* `model_max_budget` - (Optional) Per-model budget limits (map of model name to float).
* `model_rpm_limit` - (Optional) Per-model RPM limits (map of model name to int).
* `model_tpm_limit` - (Optional) Per-model TPM limits (map of model name to int).
* `blocked` - (Optional) Whether the project is blocked from making requests.

## Attribute Reference

* `id` - The project ID assigned by LiteLLM.
* `created_at` - Timestamp when the project was created.
* `updated_at` - Timestamp when the project was last updated.
* `created_by` - User who created the project.
* `updated_by` - User who last updated the project.

## Import

Projects can be imported using their project ID:

```shell
terraform import litellm_project.example <project-id>
```
