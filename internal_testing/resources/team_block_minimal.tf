# litellm_team_block - Minimal
# Blocks a team

resource "litellm_team_block" "minimal" {
  team_id = litellm_team.minimal.id
}

output "team_block_minimal_id" {
  value = litellm_team_block.minimal.id
}

output "team_block_minimal_blocked" {
  value = litellm_team_block.minimal.blocked
}
