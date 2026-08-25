# Deprecated compatibility fields are accepted only at their harmless defaults.
resource "litellm_organization" "compatibility_defaults" {
  organization_alias = "test-org-compatibility-defaults"
  blocked            = false
  tags               = []
}
