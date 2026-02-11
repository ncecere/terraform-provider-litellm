# data.litellm_users - Lists all users

data "litellm_users" "all" {
}

output "ds_users_list" {
  value = data.litellm_users.all
}
