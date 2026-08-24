# litellm_team - Default recurring team-member budget
#
# Exercises team_member_budget_duration for memberships created after the team
# default is configured.
#
# Run with: make smoke resources=team_member_budget_duration.tf

resource "litellm_team" "member_budget_duration" {
  team_alias                  = "test-team-member-budget-duration"
  team_member_budget          = 25.0
  team_member_budget_duration = "30d"
}

output "team_member_budget_duration_id" {
  value = litellm_team.member_budget_duration.id
}

output "team_member_budget_duration_value" {
  value = litellm_team.member_budget_duration.team_member_budget_duration
}
