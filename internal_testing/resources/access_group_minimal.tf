# litellm_access_group - Minimal
# All attributes are required. Pair with model_minimal.tf.

resource "litellm_access_group" "minimal" {
  access_group = "test-access-group-minimal"
  model_names  = [litellm_model.minimal.model_name]
}

output "access_group_minimal_id" {
  value = litellm_access_group.minimal.id
}
