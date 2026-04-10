# litellm_policy - Minimal
# Only required attributes

resource "litellm_policy" "minimal" {
  policy_name = "tf-test-policy-minimal"
}

output "policy_minimal_id" {
  value = litellm_policy.minimal.id
}
