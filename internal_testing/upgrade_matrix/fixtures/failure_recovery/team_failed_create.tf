variable "recover_create" {
  type    = bool
  default = false
}

resource "litellm_team" "failed_create" {
  team_alias = var.recover_create ? "issue210-team-recovered" : "issue210-team-disabled"
}
