# litellm_access_group - Full
# All attributes populated (both are required)

resource "litellm_access_group" "full" {
  access_group = "test-access-group-full"
  model_names  = ["gpt-4o", "gpt-4o-mini", "claude-3-sonnet"]
}

output "access_group_full_id" {
  value = litellm_access_group.full.id
}
