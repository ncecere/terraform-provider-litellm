# Canonical v2.0.1-compatible credential upgrade fixture.
resource "litellm_credential" "upgrade" {
  credential_name = "issue210-credential-upgrade"
  credential_info = {
    description = "Issue 210 upgrade fixture"
  }
  credential_values = {
    api_key = "issue210-placeholder-value"
  }
}

output "issue210_credential_upgrade_id" {
  value = litellm_credential.upgrade.id
}
