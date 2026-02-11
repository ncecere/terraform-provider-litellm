# data.litellm_keys - Lists all keys

data "litellm_keys" "all" {
}

output "ds_keys_list" {
  value = data.litellm_keys.all
}
