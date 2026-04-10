# data.litellm_policy_attachments - Lists all policy attachments

data "litellm_policy_attachments" "all" {
}

output "ds_policy_attachments_total_count" {
  value = data.litellm_policy_attachments.all.total_count
}
