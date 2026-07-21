# litellm_team - team_member_budget_duration
#
# Exercises the team_member_budget_duration attribute added in PR #113: the
# per-member in-team budget reset interval, applied to all members of the team.
#
# Run with:  make smoke resources=team_member_budget_duration.tf

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
