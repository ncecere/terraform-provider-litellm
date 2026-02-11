# litellm_access_group - Minimal
# All attributes are required

resource "litellm_access_group" "minimal" {
  access_group = "test-access-group-minimal"
  model_names  = ["gpt-4o-mini"]
}

output "access_group_minimal_id" {
  value = litellm_access_group.minimal.id
}
