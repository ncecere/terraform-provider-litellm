# litellm_organization_member - Full
# All attributes populated. LiteLLM's member_add resolves user_email with a
# unique-field query on a non-unique column and returns HTTP 500 unless
# user_id names an existing user, so the user is created first.

resource "litellm_user" "org_member_full" {
  user_id    = "test-member-user-full"
  user_email = "orgmember@example.com"
}

resource "litellm_organization_member" "full" {
  organization_id            = litellm_organization.full.id
  user_id                    = litellm_user.org_member_full.user_id
  user_email                 = litellm_user.org_member_full.user_email
  role                       = "org_admin"
  max_budget_in_organization = 500.0
}

output "org_member_full_id" {
  value = litellm_organization_member.full.id
}
