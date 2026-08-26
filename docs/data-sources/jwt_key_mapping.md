# litellm_jwt_key_mapping (Data Source)

Reads one LiteLLM JWT key mapping by its authoritative UUID.

```hcl
data "litellm_jwt_key_mapping" "application" {
  id = "01234567-89ab-4cde-8f01-23456789abcd"
}
```

## Argument

* `id` - (Required) Canonical lowercase mapping UUID.

## Attributes

* `jwt_claim_name` - Claim name; the empty string is source-valid.
* `jwt_claim_value` - (Sensitive) String claim value; the empty string is source-valid.
* `description` - Nullable description.
* `is_active` - Active state.
* `created_at`, `updated_at` - RFC 3339 timestamps.
* `created_by`, `updated_by` - (Sensitive) Nullable provenance.

The API intentionally omits the virtual-key token and hash. The data source accepts only the exact v1.98 mapping object and rejects alternate envelopes or malformed present fields.

`Sensitive` controls Terraform display redaction, not encryption. LiteLLM returns the claim value as plaintext, so it is stored as plaintext in Terraform state. Protect state, backups, plans, and state-service access accordingly.
