# litellm_project Data Source

Retrieves information about a LiteLLM Project.

## Example Usage

```hcl
data "litellm_project" "example" {
  id = "proj-abc-123"
}

output "project_name" {
  value = data.litellm_project.example.project_alias
}

output "project_team" {
  value = data.litellm_project.example.team_id
}
```

## Argument Reference

* `id` - (Required) The project ID to look up.

## Attribute Reference

* `project_alias` - Human-friendly name for the project.
* `description` - Description of the project.
* `team_id` - The team ID this project belongs to.
* `models` - Models the project can access.
* `metadata` - Project metadata.
* `tags` - Tags associated with the project.
* `blocked` - Whether the project is blocked.
* `spend` - Total spend for this project.
* `model_rpm_limit` - Per-model RPM limits.
* `model_tpm_limit` - Per-model TPM limits.
* `created_at` - Timestamp when the project was created.
* `updated_at` - Timestamp when the project was last updated.
* `created_by` - User who created the project.
* `updated_by` - User who last updated the project.
