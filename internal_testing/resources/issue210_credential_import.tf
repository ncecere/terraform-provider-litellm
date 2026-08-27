# Metadata-only source-free import does not claim ownership of masked secrets.
resource "litellm_credential" "upgrade" {
  credential_name = "issue210-credential-upgrade"
  credential_info = {
    description = "Issue 210 upgrade fixture"
  }
}
