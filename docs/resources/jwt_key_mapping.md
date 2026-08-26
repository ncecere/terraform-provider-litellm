# litellm_jwt_key_mapping (Resource)

Manages a LiteLLM v1.98 JWT claim-to-existing-virtual-key mapping.

## Example Usage

```hcl
resource "litellm_key" "application" {}

resource "litellm_jwt_key_mapping" "application" {
  jwt_claim_name  = "sub"
  jwt_claim_value = var.oidc_subject
  key_wo          = litellm_key.application.key
  key_wo_version  = "1"
  description     = "Application OIDC subject"
  is_active       = true
}
```

Create and key rotation require Terraform or compatible OpenTofu write-only attribute support (version 1.11 or later). The mapping points at an existing virtual key; it does not create a key or expose a generated token.

## Arguments

* `jwt_claim_name` - (Required on create, ForceNew) Non-empty JWT claim name. LiteLLM v1.98 models claim names as strings.
* `jwt_claim_value` - (Required on create, ForceNew, Sensitive) Non-empty string claim value. The provider never includes it in diagnostics.
* `key_wo` - (Required on create and rotation, Sensitive, Write-only) Raw existing LiteLLM virtual key. It is sent only on create or when `key_wo_version` changes and is never persisted by this resource.
* `key_wo_version` - (Required with `key_wo`) Persisted version or nonce. Change it when `key_wo` changes; LiteLLM updates the same mapping UUID. Because the endpoint intentionally returns neither the token nor its hash, Terraform owns this rotation nonce but cannot independently read the key back.
* `description` - (Optional) Nullable description. For a provider-created or previously configured description, assigning `null` sends an explicit JSON null clear. An imported omitted description remains API-owned; configure a non-null value to transfer ownership before a later null clear.
* `is_active` - (Optional) Active state. `false` is sent explicitly. Omitted imported state remains API-owned.

## Attributes

* `id` - Authoritative mapping UUID.
* `created_at`, `updated_at` - RFC 3339 timestamps.
* `created_by`, `updated_by` - (Sensitive) Nullable LiteLLM provenance.

LiteLLM does not return a token, token hash, or generated secret from any mapping read endpoint, so none is exposed.

## Import

Import uses exactly the canonical lowercase UUID returned by LiteLLM:

```shell
terraform import litellm_jwt_key_mapping.application 01234567-89ab-4cde-8f01-23456789abcd
```

Import reads the API-owned claim pair, description, active state, timestamps, and provenance. Omitted mutable leaves remain API-owned until configured. To rotate an imported mapping, configure both `key_wo` and a new `key_wo_version`.

## Lifecycle and failure safety

The database-generated UUID is the only resource identity. Claim name and value are immutable because v1.98 update cannot change them, while the database uniquely constrains their pair. Reads remove state only for an exact HTTP 404. Delete requires a successful delete or exact 404 and then an exact-404 info read before state is discarded.

Mutations are single-attempt. A create error retains state only when a canonical response UUID was confirmed. Update failures retain prior state and private ownership. Valid mutation responses and authoritative info reads must preserve UUID and claim identity; malformed 2xx objects, alternate envelopes, and stale reads fail closed without publishing false convergence. Sensitive request/response details are omitted from diagnostics.
