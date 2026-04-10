# litellm_policy - Full
# Parent + child policy with inheritance, guardrails, condition, and pipeline

resource "litellm_policy" "full_parent" {
  policy_name = "tf-test-policy-full-parent"
  description = "Parent policy for full inheritance smoke test"

  guardrails_add = ["pii_masking"]
}

resource "litellm_policy" "full" {
  policy_name = "tf-test-policy-full"
  description = "Child policy with inheritance managed by terraform smoke test"
  inherit     = litellm_policy.full_parent.policy_name

  guardrails_add    = ["pii_masking", "prompt_injection"]
  guardrails_remove = ["legacy_guardrail"]

  condition = {
    model = "gpt-4.*"
  }

  pipeline = jsonencode({
    mode = "pre_call"
    steps = [
      {
        guardrail = "pii_masking"
        on_pass   = "next"
        on_fail   = "block"
      }
    ]
  })
}

output "policy_full_parent_id" {
  value = litellm_policy.full_parent.id
}

output "policy_full_id" {
  value = litellm_policy.full.id
}

output "policy_full_version_status" {
  value = litellm_policy.full.version_status
}
