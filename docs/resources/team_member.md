# litellm_team_member Resource

Manages individual team member configurations in LiteLLM. This resource allows you to add, update, and remove individual team members with specific permissions and budget limits.

## Example Usage

### Basic Usage

```hcl
resource "litellm_team_member" "engineer" {
  team_id            = litellm_team.engineering.id
  user_id            = "user_3"
  user_email         = "engineer@example.com"
  role               = "user"
  max_budget_in_team = 200.0
}
```

### Without Budget Limit

```hcl
resource "litellm_team_member" "admin_user" {
  team_id    = litellm_team.engineering.id
  user_id    = "admin_1"
  user_email = "admin@example.com"
  role       = "admin"
  # max_budget_in_team is optional
}
```

## Argument Reference

The following arguments are supported:

* `team_id` - (Required) The ID of the team this member belongs to.

* `user_id` - (Required) Unique identifier for the user.

* `user_email` - (Required) Email address of the user.

* `role` - (Required) The role of the team member. Valid values are:
  * `org_admin` - Organization administrator with full access
  * `internal_user` - Internal user with standard access
  * `internal_user_viewer` - Internal user with read-only access
  * `admin` - Team administrator
  * `user` - Standard team user

* `max_budget_in_team` - (Optional) Maximum budget allocated to this team member within the team's budget. If not specified, no budget limit is applied to this member.

## Attribute Reference

In addition to the arguments above, the following attributes are exported:

* `id` - The unique identifier for the team member configuration. This is typically a composite of the team_id and user_id.

## Import

Team members can be imported using the format `team_id:user_id`:

```shell
terraform import litellm_team_member.engineer <team_id>:<user_id>
```

Note: The team_id and user_id should match the values used in the resource configuration.

## Notes

- This resource manages individual team members one at a time. For bulk operations, consider using the `litellm_team_member_add` resource instead.
- If a user doesn't exist, a new user row will be added to the User Table when the member is created.
- Only proxy_admin or admin of the team are allowed to access the underlying endpoints.
- Updates to team members require all fields (including role) to be properly set, as the API expects complete member information.

## Security Note

Ensure that sensitive information such as user emails and IDs are handled securely. It's recommended to use variables or a secure secret management solution rather than hardcoding these values in your Terraform configuration files.
