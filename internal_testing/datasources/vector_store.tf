# data.litellm_vector_store - Looks up a vector store by vector_store_id
# Note: vector_store_id must reference an existing vector store

data "litellm_vector_store" "lookup" {
  vector_store_id = litellm_vector_store.minimal.vector_store_id
}

output "ds_vector_store_name" {
  value = data.litellm_vector_store.lookup.vector_store_name
}

output "ds_vector_store_provider" {
  value = data.litellm_vector_store.lookup.custom_llm_provider
}

output "ds_vector_store_description" {
  value = data.litellm_vector_store.lookup.vector_store_description
}

output "ds_vector_store_created_at" {
  value = data.litellm_vector_store.lookup.created_at
}
