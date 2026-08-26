# Canonical v2.0.1-compatible Enterprise project upgrade fixture.
resource "litellm_project" "upgrade" {
  project_alias = "issue210-project-upgrade"
  team_id       = litellm_team.minimal.id
}

output "issue210_project_upgrade_id" {
  value = litellm_project.upgrade.id
}
