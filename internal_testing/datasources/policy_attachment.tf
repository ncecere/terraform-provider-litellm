# data.litellm_policy_attachment - Looks up an attachment by attachment_id

data "litellm_policy_attachment" "lookup" {
  attachment_id = litellm_policy_attachment.global.id
}

output "ds_policy_attachment_policy_name" {
  value = data.litellm_policy_attachment.lookup.policy_name
}
