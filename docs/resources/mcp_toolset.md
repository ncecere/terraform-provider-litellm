# litellm_mcp_toolset (Resource)

Manages an MCP toolset definition in LiteLLM. A toolset is an unordered set of MCP server and tool pairs.

The toolset resource manages only the definition. Assign its `toolset_id` through `mcp_toolset_ids` on a key or team resource.

## Example usage

```hcl
resource "litellm_mcp_toolset" "incident_response" {
  toolset_name = "incident-response"
  description  = "Read-only incident response tools"

  tools = [
    {
      server_id = "pagerduty-server-id"
      tool_name = "list_incidents"
    },
    {
      server_id = "datadog-server-id"
      tool_name = "search_datadog_logs"
    }
  ]
}
```

An empty toolset is valid:

```hcl
resource "litellm_mcp_toolset" "empty" {
  toolset_name = "empty"
}
```

## Assign toolsets

Assign a toolset directly to a key:

```hcl
resource "litellm_key" "incident_response" {
  key_alias       = "incident-response"
  mcp_toolset_ids = [litellm_mcp_toolset.incident_response.toolset_id]
}
```

LiteLLM treats a nonempty team assignment as a ceiling for keys in that team. The team assignment does not grant the toolset to its keys automatically, and a null or empty team assignment imposes no ceiling, so clearing the team set to `[]` removes the ceiling rather than denying all toolsets. Configure the toolset on both resources:

```hcl
resource "litellm_team" "responders" {
  team_alias       = "Responders"
  mcp_toolset_ids  = [litellm_mcp_toolset.incident_response.toolset_id]
}

resource "litellm_key" "responder" {
  key_alias       = "responder"
  team_id         = litellm_team.responders.id
  mcp_toolset_ids = [litellm_mcp_toolset.incident_response.toolset_id]
}
```

User toolset assignments are out of scope for this resource. LiteLLM v1.98 stores them and returns them from `/v2/user/info`, but the provider does not manage user assignments; managing them is a separate resource-scope decision.

For each target resource, omit `mcp_toolset_ids` to leave remote assignments unmanaged. Set it to `[]` to clear the complete toolset assignment while preserving other object permissions. Assignment order is not significant.

Importing a key or team reads its current assignments into the initial state. If the post-import configuration omits `mcp_toolset_ids`, the next apply removes only the imported Terraform value and leaves the LiteLLM assignments unchanged.

LiteLLM v1.98 does not support toolset assignments on legacy or unified access groups.

Assignment updates are transactional. If LiteLLM accepts an update but the authoritative readback fails or does not match the plan, Terraform keeps the prior state and reports an error. Run `terraform apply` again after the API is reachable; the next refresh reconciles the assignments.

## Create recovery

`toolset_name` is unique in LiteLLM. When LiteLLM accepts a create but the response is unusable, Terraform keeps a name-bound partial state and recovers the identity on the next refresh through exact-name evidence and a direct-ID read that must match the requested definition. When the create request is dispatched but no response status arrives, the toolset may or may not exist: Terraform keeps the name-bound state to block a duplicate create and asks you to confirm the toolset in LiteLLM, then import it by ID or remove the resource from state.

## Argument reference

- `toolset_name` - (Required) The unique toolset name.
- `description` - (Optional) A description of the toolset.
- `tools` - (Optional) An unordered set of MCP tools. The default is an empty set. Each entry requires these attributes:
  - `server_id` - The MCP server identifier.
  - `tool_name` - The tool name exposed by the MCP server.

The provider does not query the live MCP catalog to validate `server_id` or `tool_name`.

## Attribute reference

- `toolset_id` - The identifier assigned by LiteLLM.

## Import

Import a toolset with its LiteLLM toolset ID:

```shell
terraform import litellm_mcp_toolset.example <toolset-id>
```
