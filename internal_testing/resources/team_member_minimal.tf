# litellm_team_member - Minimal
# All attributes are required

resource "litellm_team_member" "minimal" {
  team_id    = litellm_team.minimal.id
  user_id    = "test-team-member-user"
  user_email = "teammember@example.com"
  role       = "user"
}

output "team_member_minimal_id" {
  value = litellm_team_member.minimal.id
}
