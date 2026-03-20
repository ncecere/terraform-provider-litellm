# litellm_projects Data Source

Fetches a list of all LiteLLM Projects.

## Example Usage

```hcl
data "litellm_projects" "all" {}

output "project_names" {
  value = [for p in data.litellm_projects.all.projects : p.project_alias]
}
```

## Attribute Reference

* `projects` - List of projects. Each project has the following attributes:
  * `project_id` - The unique project ID.
  * `project_alias` - Human-friendly name for the project.
  * `description` - Description of the project.
  * `team_id` - The team ID this project belongs to.
  * `blocked` - Whether the project is blocked.
  * `spend` - Total spend for this project.
  * `created_at` - Timestamp when the project was created.
  * `updated_at` - Timestamp when the project was last updated.
  * `created_by` - User who created the project.
  * `updated_by` - User who last updated the project.
