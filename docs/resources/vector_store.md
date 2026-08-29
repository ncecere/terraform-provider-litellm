# litellm_vector_store (Resource)

Manages a vector store in LiteLLM. Vector stores provide storage for embeddings used in retrieval-augmented generation (RAG) and semantic search workflows.

## Example Usage

### Minimal Configuration

```hcl
resource "litellm_vector_store" "minimal" {
  vector_store_name   = "my-vector-store"
  custom_llm_provider = "openai"
}
```

### Full Configuration

```hcl
resource "litellm_vector_store" "full" {
  vector_store_name        = "embeddings-store"
  custom_llm_provider      = "openai"
  vector_store_description = "Production vector store"
  litellm_credential_name  = "my-openai-cred"

  vector_store_metadata = {
    "environment" = "production"
    "version"     = "1"
  }

  litellm_params = {
    "embedding_model" = "text-embedding-3-small"
  }
}
```

## Argument Reference

The following arguments are supported:

- `vector_store_name` - (Required) The name of the vector store.
- `custom_llm_provider` - (Required) The LLM provider for the vector store. Supported values: `bedrock`, `openai`, `azure`, `vertex_ai`, `pgvector`.
- `vector_store_description` - (Optional) A human-readable description of the vector store. Removing it sends an explicit empty-string update.
- `vector_store_metadata` - (Optional) A complete map of string key-value pairs containing metadata for the vector store. `{}` and removal of an owned map send an explicit empty-map update. Imported metadata remains unmanaged while omitted.
- `litellm_credential_name` - (Optional) The name of the LiteLLM credential to use for authenticating with the provider. LiteLLM v1.98 does not accept this field on update, so changing or removing an owned value replaces the vector store.
- `litellm_params` - (Optional, Sensitive) A map of string key-value pairs containing additional LiteLLM-specific parameters. LiteLLM may redact credential-bearing values on read; Terraform preserves recognized masks only when prior owned values exist. Changing or removing this create-only map replaces the vector store.

## Update and Replacement Behavior

LiteLLM v1.98 updates only `custom_llm_provider`, `vector_store_name`, `vector_store_description`, and `vector_store_metadata`. Terraform never sends `litellm_credential_name` or `litellm_params` to the update endpoint because v1.98 silently ignores those fields.

Explicitly configuring an imported description or metadata map transfers ownership and enables later in-place clearing. Explicitly configuring an imported credential name or parameter map transfers ownership without mutation when the value already matches; later changes use replacement. Omitted imported values remain stable.

The public metadata and parameter types remain `map(string)` for compatibility. Nested API values are represented as canonical JSON strings on read; this version does not coerce Terraform strings into heterogeneous request objects.

Create and update operations require two stable fresh-worker reads before Terraform commits state. LiteLLM v1.98 can persist a database mutation and then report a registry-synchronization error; when the complete planned object is subsequently confirmed, Terraform recovers with a warning. Otherwise it retains prior state and fails safely for retry. Delete errors are accepted only when a fresh authoritative read confirms absence.

## Feature Availability

Vector-store management is protected by LiteLLM's `vector_stores` feature gate and may require an applicable LiteLLM license and role. Terraform reports the API's authorization or feature-gate failure without falling back to local configuration or database access.

## Attribute Reference

In addition to the arguments above, the following attributes are exported:

- `id` - The internal resource identifier.
- `vector_store_id` - The unique identifier assigned to the vector store by LiteLLM.
- `vector_store_metadata` - The metadata map, including any server-populated values.
- `litellm_params` - The LiteLLM parameters map, including any server-populated values.
- `created_at` - The timestamp when the vector store was created.

## Import

Vector stores can be imported using the vector store ID:

```shell
terraform import litellm_vector_store.example <vector-store-id>
```

The configuration must provide `vector_store_name` and `custom_llm_provider`. Omitted imported optional values remain unmanaged. If LiteLLM returns a masked `litellm_params` value with no prior Terraform value, import fails rather than storing the redaction marker as if it were the secret.

## Security Note

`litellm_params` is sensitive in Terraform output, but configured values remain in Terraform state. Protect state and plan artifacts and prefer `litellm_credential_name` where possible. LiteLLM redaction markers are never treated as imported credentials, and only matching masked leaves preserve prior owned state; visible unmasked changes remain detectable.
