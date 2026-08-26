# litellm_jwt_key_mappings (Data Source)

Reads the complete LiteLLM JWT key-mapping inventory.

```hcl
data "litellm_jwt_key_mappings" "all" {}
```

## Attributes

* `id` - Stable value `jwt-key-mappings`.
* `mappings` - Complete list sorted by mapping UUID. Each item exposes the same observable metadata as `litellm_jwt_key_mapping`; claim values and provenance are sensitive. Tokens and hashes are never returned.

LiteLLM v1.98 provides no list filters. The provider performs two bounded full scans: each requests `size=100`, follows every 1-based page, validates pagination metadata and identities, rejects duplicate/truncated pages, and retries only bounded count/page churn from page 1. It publishes the sorted list only when both scans contain identical UUID sets and semantically identical observable rows. This catches equal-count inserts/deletes and unstable page boundaries, but it is a bounded double-scan rather than an absolute transactional snapshot of the cluster.

LiteLLM accepts empty-string claim names and values; such rows remain valid inventory entries. `Sensitive` controls Terraform display redaction, not encryption. Because LiteLLM returns claim values as plaintext, they are stored as plaintext in Terraform state. Protect state, backups, plans, and state-service access accordingly.
