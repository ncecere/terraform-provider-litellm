# litellm_team_member Resource

Manages individual team member configurations in LiteLLM. This resource allows you to add, update, and remove team members with specific permissions and budget limits.

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

### Advanced Usage with Enhanced Features

```hcl
resource "litellm_team_member" "senior_dev" {
  team_id               = litellm_team.engineering.id
  user_id               = "john_doe"
  user_email            = "john.doe@company.com"
  role                  = "user"
  
  # Team-level budget
  max_budget_in_team    = 200.0
  
  # User-level budget (when update_user_record is true)
  user_max_budget       = 300.0
  budget_duration       = "1mo"
  
  # Enhanced features (all default to false for backward compatibility)
  update_user_record    = true   # Update user email/budget/role
  cascade_delete_keys   = true   # Delete API keys when removing from team
  cleanup_orphaned_user = false  # Don't delete user if removed from all teams
}
```

## Argument Reference

The following arguments are supported:

### Required Arguments

* `team_id` - (Required) The ID of the team this member belongs to. Changes to this field will force resource recreation.

* `user_id` - (Required) Unique identifier for the user. Changes to this field will force resource recreation.

* `user_email` - (Required) Email address of the user.

* `role` - (Required) The role of the team member. Valid values are:
  * `org_admin`
  * `internal_user`
  * `internal_user_viewer`
  * `admin`
  * `user`

### Optional Arguments

* `max_budget_in_team` - (Optional) Maximum budget allocated to this team member within the team's budget. Default: 0 (no limit).

* `user_max_budget` - (Optional) Maximum budget for the user at the user level. Only applied when `update_user_record` is true. If not specified, uses `max_budget_in_team`.

* `budget_duration` - (Optional) Budget duration (e.g., '1mo', '1d'). Default: `"1mo"`.

* `update_user_record` - (Optional) Whether to update the user record with email and budget information. Default: `false` (preserves backward compatibility).

* `cascade_delete_keys` - (Optional) Whether to delete user's API keys when removing from team. Default: `false` (preserves backward compatibility).

* `cleanup_orphaned_user` - (Optional) Whether to delete the user entirely if they have no team memberships left. Default: `false`.

## Attribute Reference

In addition to the arguments above, the following attributes are exported:

* `id` - The unique identifier for the team member configuration. This is typically a composite of the team_id and user_id.

## Behavior Details

### Enhanced Features

When using the enhanced features, the resource provides additional functionality:

1. **User Record Management** (`update_user_record = true`):
   * Automatically updates or creates user records with email, budget, and role
   * Maps `user` role to `internal_user` for LiteLLM compatibility
   * Syncs user-level data during read operations

2. **Cascading Deletion** (`cascade_delete_keys = true`):
   * Deletes all API keys associated with the user when removed from team
   * Helps maintain security by cleaning up orphaned access keys

3. **Orphaned User Cleanup** (`cleanup_orphaned_user = true`):
   * Checks if user belongs to any other teams
   * Deletes the user record if they have no remaining team memberships
   * Use with caution as this permanently removes the user

### Backward Compatibility

All enhanced features default to `false`, ensuring existing configurations continue to work without modification. Enable features selectively based on your requirements.

## Import

Team members can be imported using the format `team_id:user_id`:

```shell
terraform import litellm_team_member.engineer <team_id>:<user_id>
```

Note: The team_id and user_id should match the values used in the resource configuration.

## Security Note

Ensure that sensitive information such as user emails and IDs are handled securely. It's recommended to use variables or a secure secret management solution rather than hardcoding these values in your Terraform configuration files.
