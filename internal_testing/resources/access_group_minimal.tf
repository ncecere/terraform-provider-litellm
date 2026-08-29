# litellm_access_group - Minimal
# All attributes are required. Pair with model_access_group.tf.

resource "litellm_access_group" "minimal" {
  access_group = "test-access-group-minimal"
  model_names  = [litellm_model.access_group.model_name]
}

output "access_group_minimal_id" {
  value = litellm_access_group.minimal.id
}
