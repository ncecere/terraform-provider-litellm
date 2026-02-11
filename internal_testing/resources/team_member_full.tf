# litellm_team_member - Full
# All attributes populated

resource "litellm_team_member" "full" {
  team_id            = litellm_team.full.id
  user_id            = "test-team-member-user-full"
  user_email         = "teammemberfull@example.com"
  role               = "admin"
  max_budget_in_team = 100.0
}

output "team_member_full_id" {
  value = litellm_team_member.full.id
}
