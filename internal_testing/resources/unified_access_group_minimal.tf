# litellm_unified_access_group - Minimal
# Only the required attribute: access_group_name. Uses the current
# /v1/access_group API.

resource "litellm_unified_access_group" "minimal" {
  access_group_name = "test-unified-access-group-minimal"
}

output "unified_access_group_minimal_id" {
  value = litellm_unified_access_group.minimal.id
}
