# data.litellm_policy - Looks up a policy by policy_id

data "litellm_policy" "lookup" {
  policy_id = litellm_policy.minimal.id
}

output "ds_policy_name" {
  value = data.litellm_policy.lookup.policy_name
}

output "ds_policy_version_status" {
  value = data.litellm_policy.lookup.version_status
}
