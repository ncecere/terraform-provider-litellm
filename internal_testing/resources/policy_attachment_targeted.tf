# litellm_policy_attachment - Targeted scope

resource "litellm_policy" "attachment_targeted_policy" {
  policy_name = "tf-test-policy-attachment-targeted"
}

resource "litellm_policy_attachment" "targeted" {
  policy_name = litellm_policy.attachment_targeted_policy.policy_name
  teams       = ["default"]
  models      = ["gpt-4o"]
  tags        = ["health-*"]
}

output "policy_attachment_targeted_id" {
  value = litellm_policy_attachment.targeted.id
}
