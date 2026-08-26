variable "recover_create" {
  type    = bool
  default = false
}

resource "litellm_team" "failed_create" {
  team_alias = "issue210-team-member-failed-create"
}

resource "litellm_team_member" "failed_create" {
  team_id    = litellm_team.failed_create.id
  user_email = var.recover_create ? "issue210-recovered@example.invalid" : "not-an-email"
  role       = "user"
}
