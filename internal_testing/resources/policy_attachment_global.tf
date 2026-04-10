# litellm_policy_attachment - Global scope

resource "litellm_policy" "attachment_global_policy" {
  policy_name = "tf-test-policy-attachment-global"
}

resource "litellm_policy_attachment" "global" {
  policy_name = litellm_policy.attachment_global_policy.policy_name
  scope       = "*"
}

output "policy_attachment_global_id" {
  value = litellm_policy_attachment.global.id
}
