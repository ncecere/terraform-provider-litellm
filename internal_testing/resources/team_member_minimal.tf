# litellm_team_member - Minimal email-only identity.
# LiteLLM resolves/creates the user and the provider persists canonical user_id.

resource "litellm_team_member" "minimal" {
  team_id    = litellm_team.minimal.id
  user_email = "teammember@example.com"
  role       = "user"
}

output "team_member_minimal_id" {
  value = litellm_team_member.minimal.id
}
