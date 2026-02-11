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
- `vector_store_description` - (Optional) A human-readable description of the vector store.
- `vector_store_metadata` - (Optional) A map of string key-value pairs containing metadata for the vector store.
- `litellm_credential_name` - (Optional) The name of the LiteLLM credential to use for authenticating with the provider.
- `litellm_params` - (Optional) A map of string key-value pairs containing additional LiteLLM-specific parameters for the vector store.

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
