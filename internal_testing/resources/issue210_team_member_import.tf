# Source-free import fixture using the immutable canonical identity only.
resource "litellm_team_member" "upgrade" {
  team_id = litellm_team.minimal.id
  user_id = "issue210-team-member-upgrade"
  role    = "user"
}

output "issue210_team_member_upgrade_id" {
  value = litellm_team_member.upgrade.id
}
