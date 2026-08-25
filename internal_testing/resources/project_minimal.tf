# LiteLLM Project management requires an Enterprise license.
resource "litellm_project" "minimal" {
  team_id = litellm_team.minimal.id
}

output "project_minimal_id" {
  value = litellm_project.minimal.id
}
