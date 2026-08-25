# litellm_team_member - Full at-least-one identity compatibility fixture.
# Both identity fields must resolve to this same canonical user.

resource "litellm_team_member" "full" {
  team_id            = litellm_team.full.id
  user_id            = "test-team-member-user-full"
  user_email         = "teammemberfull@example.com"
  role               = "admin"
  max_budget_in_team = 100.0
  budget_duration    = "30d"
}

output "team_member_full_id" {
  value = litellm_team_member.full.id
}
