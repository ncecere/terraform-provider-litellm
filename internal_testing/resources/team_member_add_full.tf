# litellm_team_member_add - Full
# All attributes populated, multiple members

resource "litellm_team_member_add" "full" {
  team_id            = litellm_team.full.id
  max_budget_in_team = 75.0

  member {
    user_id    = "batch-user-1"
    user_email = "batchuser1@example.com"
    role       = "admin"
  }

  member {
    user_id    = "batch-user-2"
    user_email = "batchuser2@example.com"
    role       = "user"
  }
}

output "team_member_add_full_id" {
  value = litellm_team_member_add.full.id
}
