# litellm_jwt_key_mappings (Data Source)

Reads the complete LiteLLM JWT key-mapping inventory.

```hcl
data "litellm_jwt_key_mappings" "all" {}
```

## Attributes

* `id` - Stable value `jwt-key-mappings`.
* `mappings` - Complete list sorted by mapping UUID. Each item exposes the same observable metadata as `litellm_jwt_key_mapping`; claim values and provenance are sensitive. Tokens and hashes are never returned.

LiteLLM v1.98 provides no list filters. The provider requests `size=100`, follows every 1-based page, validates all pagination metadata and item identities, rejects duplicate/truncated pages, and retries only bounded count/page churn from page 1. It never publishes a partial list.
