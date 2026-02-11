# litellm_vector_store - Minimal
# Only required attributes

resource "litellm_vector_store" "minimal" {
  vector_store_name   = "test-vs-minimal"
  custom_llm_provider = "openai"
}

output "vector_store_minimal_id" {
  value = litellm_vector_store.minimal.id
}
