# data.litellm_user - Looks up a user by user_id
# Note: user_id must reference an existing user

data "litellm_user" "lookup" {
  user_id = litellm_user.minimal.id
}

output "ds_user_alias" {
  value = data.litellm_user.lookup.user_alias
}

output "ds_user_email" {
  value = data.litellm_user.lookup.user_email
}

output "ds_user_role" {
  value = data.litellm_user.lookup.user_role
}

output "ds_user_models" {
  value = data.litellm_user.lookup.models
}

output "ds_user_spend" {
  value = data.litellm_user.lookup.spend
}
