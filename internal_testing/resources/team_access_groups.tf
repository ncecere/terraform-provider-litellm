# litellm_team - Access group association
# Pair with model_minimal.tf and access_group_minimal.tf.
#
# Run with:
#   make smoke resources=model_minimal.tf,access_group_minimal.tf,team_access_groups.tf

resource "litellm_team" "access_groups" {
  team_alias = "test-team-access-groups"
  access_group_ids = [
    litellm_access_group.minimal.id,
  ]
}

output "team_access_group_ids" {
  value = litellm_team.access_groups.access_group_ids
}
