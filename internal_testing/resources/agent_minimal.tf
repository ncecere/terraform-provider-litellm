# litellm_agent - Minimal
# Only required attributes: agent_name and an agent_card block (name + url).

resource "litellm_agent" "minimal" {
  agent_name = "test-agent-minimal"

  agent_card {
    name = "Test Agent Minimal"
    url  = "https://agent.example.com/a2a"
  }
}

output "agent_minimal_id" {
  value = litellm_agent.minimal.id
}
