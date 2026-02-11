# litellm_team_member_add - Minimal
# Batch member add resource

resource "litellm_team_member_add" "minimal" {
  team_id = litellm_team.minimal.id

  member {
    user_email = "batchmember1@example.com"
    role       = "user"
  }
}

output "team_member_add_minimal_id" {
  value = litellm_team_member_add.minimal.id
}
