# litellm_user - Minimal
# No required attributes

resource "litellm_user" "minimal" {
  auto_create_key = false
}

output "user_minimal_id" {
  value = litellm_user.minimal.id
}
