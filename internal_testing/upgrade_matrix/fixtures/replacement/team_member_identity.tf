variable "replacement_phase" {
  type    = string
  default = "before"

  validation {
    condition     = contains(["before", "after"], var.replacement_phase)
    error_message = "replacement_phase must be before or after."
  }
}

resource "litellm_team" "replacement" {
  team_alias = "issue210-team-member-replacement"
}

resource "litellm_team_member" "replacement" {
  team_id    = litellm_team.replacement.id
  user_email = var.replacement_phase == "before" ? "issue210-before@example.invalid" : "issue210-after@example.invalid"
  role       = "user"

  lifecycle {
    create_before_destroy = true
  }
}

resource "litellm_team_member_add" "replacement_dependency" {
  team_id = litellm_team.replacement.id

  member {
    user_email = "issue210-dependent@example.invalid"
    role       = "user"
  }

  depends_on = [litellm_team_member.replacement]
}
