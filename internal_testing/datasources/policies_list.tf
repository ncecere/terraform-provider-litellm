# data.litellm_policies - Lists all policies

data "litellm_policies" "all" {
}

output "ds_policies_total_count" {
  value = data.litellm_policies.all.total_count
}
