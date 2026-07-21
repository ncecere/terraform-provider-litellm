# litellm_team - Custom team_id
#
# Exercises the user-specifiable team_id added in PR #123: when team_id is set,
# it is sent to /team/new and both `id` and `team_id` reflect that value
# (instead of an API-generated one).
#
# Run with:  make smoke resources=team_custom_id.tf

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
