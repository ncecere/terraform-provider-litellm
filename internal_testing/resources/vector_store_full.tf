# litellm_vector_store - Full
# All attributes populated

resource "litellm_vector_store" "full" {
  vector_store_name        = "test-vs-full"
  custom_llm_provider      = "openai"
  vector_store_description = "Full test vector store"
  litellm_credential_name  = "test-cred-full"

  vector_store_metadata = {
    "environment" = "testing"
    "version"     = "1"
  }

  litellm_params = {
    "embedding_model" = "text-embedding-3-small"
  }
}

output "vector_store_full_id" {
  value = litellm_vector_store.full.id
}

output "vector_store_full_created_at" {
  value = litellm_vector_store.full.created_at
}
