# data.litellm_team - Looks up a team by team_id
# Note: team_id must reference an existing team

data "litellm_team" "lookup" {
  team_id = litellm_team.minimal.id
}

output "ds_team_alias" {
  value = data.litellm_team.lookup.team_alias
}

output "ds_team_models" {
  value = data.litellm_team.lookup.models
}

output "ds_team_max_budget" {
  value = data.litellm_team.lookup.max_budget
}

output "ds_team_spend" {
  value = data.litellm_team.lookup.spend
}

output "ds_team_blocked" {
  value = data.litellm_team.lookup.blocked
}

output "ds_team_permissions" {
  value = data.litellm_team.lookup.team_member_permissions
}
