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

Create and claim-pair replacement require Terraform or compatible OpenTofu write-only attribute support (version 1.11 or later). Import, refresh, same-identity planning with a null key/version, destroy, and reads remain usable by older supported clients. The mapping points at an existing virtual key; it does not create a key or expose a generated token.

## Arguments

* `jwt_claim_name` - (Required on create, ForceNew) String JWT claim name. LiteLLM v1.98 accepts the empty string.
* `jwt_claim_value` - (Required on create, ForceNew, Sensitive) String claim value. LiteLLM v1.98 accepts the empty string. The provider never includes the configured value in diagnostics.
* `key_wo` - (Required on create and claim-pair replacement, Sensitive, Write-only) Raw existing LiteLLM virtual key. It is sent only to create the new mapping and is never persisted by this resource.
* `key_wo_version` - (Required with `key_wo` on create and claim-pair replacement) Persisted create-time version marker. An unchanged historical marker remains plannable. Adding or changing the marker while preserving the same claim pair fails before mutation with `Unsupported JWT Key Rotation`. A known claim-pair replacement may use an unchanged or changed marker only when both `key_wo` and `key_wo_version` are known, non-null, and non-empty before Terraform schedules replacement.
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

Import reads the API-owned claim pair, description, active state, timestamps, and provenance. Omitted mutable leaves remain API-owned until explicitly present in configuration. An explicitly configured description transfers ownership even when it already equals the API value; that ownership transfer performs no remote mutation, and later removal sends the documented null clear. In-place key rotation is not supported on imported or existing mappings. Replacing an imported mapping's claim pair requires explicitly configured replacement `key_wo` and `key_wo_version`; Terraform will not destroy an imported keyless mapping when those create credentials are absent or unknown.

## Lifecycle and failure safety

The database-generated UUID is the only resource identity. Claim name and value are immutable because v1.98 update cannot change them, while the database uniquely constrains their pair. A known change to either claim requires replacement and is planned only when the complete new claim pair and both replacement key arguments are already known. Unknown claim values remain non-destructive until Terraform can re-plan them. Destroy-only plans never require key material. Reads remove state only for an exact HTTP 404. Delete requires a successful delete or exact 404 and then an exact-404 info read before state is discarded.

Mutations are single-attempt. The create endpoint always creates an active row; when `is_active = false`, the provider retains the confirmed UUID, sends one controlled deactivation update, validates its response, and performs a fresh info read before completing state. A failure after UUID confirmation retains UUID-only recovery state, preventing a duplicate create on the next apply.

When a create request may have committed but no canonical UUID was received, Terraform publishes no guessed identity and does not adopt by claim pair: a concurrent creator cannot be distinguished from this request. An administrator must list JWT key mappings, locate the exact claim-name/claim-value pair, obtain its canonical UUID, and import that UUID. A later HTTP 409 can be evidence that manual recovery is required, but it is not safe identity proof.

Update failures retain prior state and private ownership. Delete endpoint 404 is not sufficient by itself: the provider still requires a singular `/info?id=...` exact-404 proof before removing state. Valid mutation responses and authoritative info reads must preserve UUID and claim identity; malformed 2xx objects, alternate envelopes, and stale observable reads fail closed without publishing false convergence. Timestamps are observable metadata, not key-rotation proof. Sensitive request/response details are omitted from diagnostics.

`Sensitive` controls Terraform CLI/UI redaction; it is not encryption. The API returns `jwt_claim_value` as plaintext, so that plaintext is necessarily stored in Terraform state for this resource and both data sources. Protect local and remote state, backups, plans, and access to state APIs accordingly. The raw `key_wo` is different: it is write-only and is not stored in resource state or plans by the provider.
