# litellm_team - Custom team_id
#
# Exercises user-specified identity: both id and team_id must reflect the exact
# value sent to /team/new instead of a provider-generated UUID.
#
# Run with: make smoke resources=team_custom_id.tf

resource "litellm_team" "custom_id" {
  team_alias = "test-team-custom-id"
  team_id    = "test-team-custom-id-smoke"
}

output "team_custom_id_id" {
  value = litellm_team.custom_id.id
}

output "team_custom_id_team_id" {
  value = litellm_team.custom_id.team_id
}
