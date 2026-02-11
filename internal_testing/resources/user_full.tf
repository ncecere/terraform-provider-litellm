# litellm_user - Full
# All attributes populated

resource "litellm_user" "full" {
  user_id         = "test-user-full"
  user_alias      = "Test User Full"
  user_email      = "testuser@example.com"
  user_role       = "internal_user"
  max_budget      = 200.0
  budget_duration = "30d"
  tpm_limit       = 50000
  rpm_limit       = 500
  auto_create_key = true

  teams  = []
  models = ["gpt-4o", "gpt-4o-mini"]

  metadata = {
    "department" = "engineering"
  }
}

output "user_full_id" {
  value = litellm_user.full.id
}

output "user_full_key" {
  value     = litellm_user.full.key
  sensitive = true
}
