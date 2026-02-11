# litellm_team - Minimal
# Only required attributes

resource "litellm_team" "minimal" {
  team_alias = "test-team-minimal"
}

output "team_minimal_id" {
  value = litellm_team.minimal.id
}
