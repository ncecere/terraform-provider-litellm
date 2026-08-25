# litellm_prompt (Resource)

Manages a LiteLLM prompt. Prompts allow you to store reusable prompt templates that can be used across different models and applications via the LiteLLM prompt manager.

## Example Usage

### Minimal Example

```hcl
resource "litellm_prompt" "minimal" {
  prompt_id          = "my-prompt"
  prompt_integration = "dotprompt"
}
```

### Full Example with Dotprompt Content

```hcl
resource "litellm_prompt" "full" {
  prompt_id          = "customer-support"
  environment        = "production"
  prompt_integration = "dotprompt"
  prompt_type        = "db"

  dotprompt_content = <<-EOT
    ---
    model: gpt-4o
    ---
    You are a helpful assistant. Answer the user's question concisely.
    {{question}}
  EOT

  ignore_prompt_manager_model           = false
  ignore_prompt_manager_optional_params = false
}
```

### Prompt with API Configuration

```hcl
resource "litellm_prompt" "with_api" {
  prompt_id          = "external-prompt"
  prompt_integration = "dotprompt"
  prompt_type        = "db"

  api_base = "https://my-litellm-instance.example.com"
  api_key  = var.litellm_api_key

  dotprompt_content = <<-EOT
    ---
    model: gpt-4o-mini
    ---
    Summarize the following text in {{language}}:
    {{text}}
  EOT

  ignore_prompt_manager_model = true
}
```

## Argument Reference

The following arguments are supported:

### Required

* `prompt_id` - (Required, ForceNew) Unique identifier for the prompt. Changing this forces creation of a new resource.
* `prompt_integration` - (Required) The prompt integration type (e.g., `"dotprompt"`).

### Optional

* `environment` - (Optional, ForceNew) Environment-scoped ownership dimension. Defaults to `"development"`. Changing it replaces the resource without deleting versions in any other environment.
* `dotprompt_content` - (Optional) The dotprompt-formatted content for the prompt. Supports YAML frontmatter for model configuration and Mustache-style `{{variable}}` template syntax.
* `prompt_type` - (Optional) The prompt definition location. LiteLLM v1.98 accepts `"config"` or `"db"`; API-managed creates use `"db"` when omitted. The provider rejects new `config` creates because v1.98 cannot update or delete them, while existing/imported config prompts remain readable.
* `api_base` - (Optional) API base URL for the prompt manager endpoint.
* `api_key` - (Optional, Sensitive) API key for authenticating with the prompt manager endpoint.
* `provider_specific_query_params` - (Optional) Provider-specific query parameters to pass through.
* `ignore_prompt_manager_model` - (Optional) When `true`, ignores the model specified in the prompt manager and uses the caller's model instead.
* `ignore_prompt_manager_optional_params` - (Optional) When `true`, ignores optional parameters specified in the prompt manager.

## Attribute Reference

In addition to all arguments above, the following attributes are exported:

* `id` - The base prompt identifier. This remains equal to `prompt_id` for backward compatibility; the full managed identity is the pair `(prompt_id, environment)`.
* `version` - The latest positive version owned in this environment. Each successful update appends a new version.
* `created_at` - Creation timestamp of the selected latest version.
* `updated_at` - Last-update timestamp of the selected latest version.

## Import

Prompts can be imported with a collision-safe environment-qualified ID:

```shell
# prompt_id = "my-prompt", environment = "production"
terraform import litellm_prompt.example v1.bXktcHJvbXB0.cHJvZHVjdGlvbg
```

The grammar is `v1.<base64url(prompt_id)>.<base64url(environment)>`, without padding. Historical bare prompt IDs remain accepted and import the `development` environment:

```shell
terraform import litellm_prompt.example my-prompt
```

If a historical prompt ID itself has the exact canonical-looking `v1.<part>.<part>` shape, disambiguate it with `legacy.<base64url(prompt_id)>`; for example, `v1.YQ.Yg` is imported as `legacy.djEuWVEuWWc`.

## Environment and Version Ownership

A `litellm_prompt` resource represents the mutable latest logical prompt for one `(prompt_id, environment)` pair. Create first verifies that the scoped identity is absent (existing prompts must be imported), then `POST` creates version 1 in that environment, and each Terraform update uses LiteLLM's full `PUT` contract to append the next version. Refresh always selects the latest version with an explicit environment query. Terraform does not model immutable individual versions because LiteLLM v1.98 cannot delete one version: environment-scoped DELETE removes the complete version history for that logical prompt.

Before a full-version update, the provider reads the current scoped prompt and refuses to proceed if non-null integration parameters are not represented by the schema or if the remote prompt has an API key that Terraform does not own. This prevents imported credentials or integration-specific fields from being silently dropped by LiteLLM's full `PUT`. Configure `api_key` explicitly before updating an imported credential-bearing prompt; prompts with other unmodeled parameters remain read/import-only until schema support is added.

Destroy always sends `?environment=...` and confirms that scoped identity is absent. It never intentionally sends LiteLLM's dangerous unscoped delete, which would remove every environment. If v1.98 returns 404 because a worker lost its process-local registry key while the DB row remains, the provider reinitializes the exact scoped DB prompt with a no-content-change PATCH, retries DELETE once, and still requires an absence read before removing state.

LiteLLM v1.98's process-local prompt registry keys omit environment and can collide when two environments use the same base ID/version. The provider uses explicit environment-scoped info reads and version inventory to recover deterministic API state, but applications should still validate runtime prompt resolution across workers.

## Notes

* Prompt IDs are unique only within an environment/version tuple, not globally.
* Existing configurations that omit `environment` continue to own `development`, and `id` remains the base `prompt_id`.
* `prompt_type = "config"` is accepted by LiteLLM but config prompts cannot be updated or deleted through v1.98 management APIs. Existing config prompts are therefore read/import-only and destroy safely retains state when LiteLLM refuses deletion; use `db` (the default) for managed resources.
* Prompt writes require LiteLLM `proxy_admin`; reads require an admin-view role. Prompt management is not Enterprise-license gated but database-backed writes require LiteLLM database support.
* Use heredoc syntax (`<<-EOT`) for multi-line dotprompt content.
* Dotprompt content supports YAML frontmatter (between `---` delimiters) for specifying model and parameter defaults.
* Template variables use Mustache-style `{{variable}}` syntax within the prompt body.
