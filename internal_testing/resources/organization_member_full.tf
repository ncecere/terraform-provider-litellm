# litellm_organization_member - Full
# All attributes populated

resource "litellm_organization_member" "full" {
  organization_id            = litellm_organization.full.id
  user_id                    = "test-member-user-full"
  user_email                 = "orgmember@example.com"
  role                       = "org_admin"
  max_budget_in_organization = 500.0
}

output "org_member_full_id" {
  value = litellm_organization_member.full.id
}
