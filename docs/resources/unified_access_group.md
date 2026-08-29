# litellm_unified_access_group

Manages a LiteLLM Access Group through the `/v1/access_group` API. These are the Access Groups shown in the LiteLLM UI.

This resource is separate from `litellm_access_group`, which manages the older model-only access-group API.

## Minimal Example

```hcl
resource "litellm_unified_access_group" "minimal" {
  access_group_name = "Engineering"
}
```

## Full Example

```hcl
variable "service_key" {
  type      = string
  sensitive = true
}

resource "litellm_key" "generated" {
  key_alias = "engineering-generated"
}

resource "litellm_key" "service" {
  key_wo         = var.service_key
  key_wo_version = "1"
  key_alias      = "engineering-service"
}

resource "litellm_unified_access_group" "engineering" {
  access_group_name = "Engineering"
  description       = "Engineering access group"

  access_model_names = [
    "gpt-4o",
    "claude-sonnet-4.5",
  ]

  access_mcp_server_ids = []
  access_agent_ids      = []
  assigned_team_ids     = [litellm_team.engineering.team_id]

  # litellm_key.id is a non-sensitive sha256:<64-hex> management ID.
  assigned_key_ids = [
    litellm_key.generated.id,
    litellm_key.service.id,
  ]
}
```

Use an explicit empty list to detach every key managed by the resource:

```hcl
assigned_key_ids = []
```

## Argument Reference

* `access_group_name` - (Required) Display name of the access group.
* `description` - (Optional) Description of the access group.
* `access_model_names` - (Optional) Model names this access group grants access to.
* `access_mcp_server_ids` - (Optional) MCP server IDs this access group grants access to.
* `access_agent_ids` - (Optional) Agent IDs this access group grants access to.
* `assigned_team_ids` - (Optional) Team IDs assigned to this access group.
* `assigned_key_ids` - (Optional) Key membership as a Terraform `list(string)`. Prefer `litellm_key.<name>.id`, which uses `sha256:<64-hex>`. Valid bare hashes, prefixed hashes, uppercase hexadecimal digits, and their historical representations remain accepted. Raw API keys and malformed values are rejected without being hashed.

LiteLLM treats key assignment as unordered membership. The provider sends sorted, deduplicated, lowercase bare hashes. When unique normalized membership is unchanged, Terraform preserves the configured or prior list exactly, including order, duplicates, prefix casing, and hexadecimal casing. Real membership drift is exposed as a deterministic sorted list of deduplicated bare hashes. Existing indexing, `concat(...)`, `list(string)` module inputs, state, and imports therefore remain compatible.

The LiteLLM API supports assigning Access Groups to teams and keys. Project assignment is not exposed by `/v1/access_group`.

## Key Assignment Verification and Security

The provider preflights configured keys and reads both durable sides of every candidate assignment. The access-group `assigned_key_ids` row proves only the group side. The complete, paginated `/key/list?access_group_id=...` inventory and echo-bound `/key/info` prove only the key side. A key is confirmed in resource state or import only when its normalized hash appears on **both** rows. Group-only and key-only values are excluded from state and reported as ambiguity/drift; raw tokens, key-name suffixes, malformed values, and unrelated echoes are never absorbed.

LiteLLM v1.98 updates key rows from the delta against the access-group row, so one-sided repair is direction-specific:

* A group-only desired attachment is removed from the group row, confirmed detached on both rows, then re-added to force the missing key delta. Other group assignments are preserved during this bounded reset.
* A key-only desired attachment is added through the normal access-group update, which supplies the missing group-side delta.
* A group-only desired detach is removed through the normal access-group update.
* A key-only desired detach is never repaired by temporarily adding the group side, because that could create authorization. The provider instead reads the key's complete `access_group_ids`, calls the public v1.98 `/key/update` API with the normalized hash and the full list minus only this group, and verifies that every unrelated group was preserved. If the server does not support that exact operation, Terraform retains the resource identity, reports an error, and requires operator repair rather than broadening authorization.

All multi-step outcomes are read back from both rows. Terraform retains only their confirmed intersection and reports any partial or one-sided result. Create recovery also refuses preexisting or multiple exact-name groups. After a possibly accepted create returns an unusable response, successful empty exact-name results are retried for a small bounded propagation window. A unique exact-name and exact-known-configuration candidate can be retained only with an explicit uncertain-ownership error; exhaustion instructs the operator to inspect and import before retrying so a delayed commit is not orphaned or duplicated.

Two-sided row verification confirms durable database state only. LiteLLM v1.98 provides no API that invalidates every worker's in-memory key cache. After an attach, another worker may continue denying access; after a security-sensitive detach, another worker may continue authorizing the previous access until that worker's configured cache TTL expires. The provider warning does not promise cross-worker runtime authorization convergence, prescribe a fixed wait, or replace operational cache controls.

## Attribute Reference

In addition to the arguments above, the following attributes are exported:

* `id` - Access group ID.
* `access_group_id` - Access group ID.
* `created_at` - Timestamp when the access group was created.
* `created_by` - User who created the access group.
* `updated_at` - Timestamp when the access group was updated.
* `updated_by` - User who last updated the access group.

## Import

Import an Access Group by ID:

```shell
terraform import litellm_unified_access_group.engineering <access_group_id>
```

The first refresh builds candidates from the access-group response and complete paginated key inventory, then includes only the normalized intersection confirmed by the access-group row and echo-bound `/key/info` key row. One-sided import candidates are excluded and reported as ambiguity/drift. Confirmed imported assignments use deterministic bare hashes. Configuration may replace them with equivalent `litellm_key.<name>.id` values; once normalized membership matches, Terraform preserves the configured list representation.
