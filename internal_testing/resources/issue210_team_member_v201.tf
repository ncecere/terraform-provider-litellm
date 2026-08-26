# Canonical v2.0.1-compatible team-member upgrade fixture.
resource "litellm_team_member" "upgrade" {
  team_id    = litellm_team.minimal.id
  user_id    = "issue210-team-member-upgrade"
  user_email = "issue210-team-member@example.invalid"
  role       = "user"
}

output "issue210_team_member_upgrade_id" {
  value = litellm_team_member.upgrade.id
}
