# data.litellm_teams - Lists all teams

data "litellm_teams" "all" {
}

output "ds_teams_list" {
  value = data.litellm_teams.all
}
