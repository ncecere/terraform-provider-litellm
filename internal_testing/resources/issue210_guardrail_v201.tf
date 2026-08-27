# Canonical v2.0.1-compatible guardrail upgrade fixture.
resource "litellm_guardrail" "upgrade" {
  guardrail_name = "issue210-guardrail-upgrade"
  guardrail      = "aporia"
  mode           = "pre_call"
}

output "issue210_guardrail_upgrade_id" {
  value = litellm_guardrail.upgrade.id
}
