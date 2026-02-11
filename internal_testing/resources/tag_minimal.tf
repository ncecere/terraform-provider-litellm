# litellm_tag - Minimal
# Only required attributes

resource "litellm_tag" "minimal" {
  name = "test-tag-minimal"
}

output "tag_minimal_id" {
  value = litellm_tag.minimal.id
}
