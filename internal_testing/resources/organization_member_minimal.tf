# litellm_organization_member - Minimal
# Requires an organization to exist first
# Either user_id or user_email must be provided

resource "litellm_organization_member" "minimal" {
  organization_id = litellm_organization.minimal.id
  user_id         = "test-member-user"
  role            = "internal_user"
}

output "org_member_minimal_id" {
  value = litellm_organization_member.minimal.id
}
