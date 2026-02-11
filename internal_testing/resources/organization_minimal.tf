# litellm_organization - Minimal
# Only required attributes

resource "litellm_organization" "minimal" {
  organization_alias = "test-org-minimal"
}

output "org_minimal_id" {
  value = litellm_organization.minimal.id
}
