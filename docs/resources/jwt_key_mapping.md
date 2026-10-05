# litellm_jwt_key_mapping (Resource)

Manages a LiteLLM JWT claim-to-existing-virtual-key mapping, optionally scoped to one JWT issuer.

## Example Usage

```hcl
resource "litellm_key" "application" {}

resource "litellm_jwt_key_mapping" "application" {
  jwt_issuer      = "https://login.example.com/tenant" # optional; omit for the global scope
  jwt_claim_name  = "sub"
  jwt_claim_value = var.oidc_subject
  key_wo          = litellm_key.application.key
  key_wo_version  = "1"
  description     = "Application OIDC subject"
  is_active       = true
}
```

Create and identity replacement require Terraform or compatible OpenTofu write-only attribute support (version 1.11 or later). Import, refresh, same-identity planning with a null key/version, destroy, and reads remain usable by older supported clients. The mapping points at an existing virtual key; it does not create a key or expose a generated token.

## Arguments

* `jwt_issuer` - (Optional, ForceNew when changed) JWT issuer (`iss`) that scopes the mapping. Null is LiteLLM's global scope: the mapping matches tokens from any issuer that has no issuer-specific mapping for the same claim. When omitted, the existing scope is preserved, so imported and pre-2.2.0 mappings keep their scope. Changing a configured value, including from the global scope, replaces the mapping and therefore requires `key_wo` and `key_wo_version`. The empty string is rejected because LiteLLM stores it as the global scope. Requires LiteLLM 1.104.0 or later; older servers either reject the field or create a global mapping, which the provider reports as a failed create.
* `jwt_claim_name` - (Required on create, ForceNew) String JWT claim name. LiteLLM accepts the empty string.
* `jwt_claim_value` - (Required on create, ForceNew, Sensitive) String claim value. LiteLLM accepts the empty string. The provider never includes the configured value in diagnostics.
* `key_wo` - (Required on create and identity replacement, Sensitive, Write-only) Raw existing LiteLLM virtual key. It is sent only to create the new mapping and is never persisted by this resource.
* `key_wo_version` - (Required with `key_wo` on create and identity replacement) Persisted create-time version marker. An unchanged historical marker remains plannable. Adding or changing the marker while preserving the same claim pair fails before mutation with `Unsupported JWT Key Rotation`. A known claim-pair replacement may use an unchanged or changed marker only when both `key_wo` and `key_wo_version` are known, non-null, and non-empty before Terraform schedules replacement.
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

Import reads the API-owned issuer scope, claim pair, description, active state, timestamps, and provenance. Omitted mutable leaves remain API-owned until explicitly present in configuration. An explicitly configured description transfers ownership even when it already equals the API value; that ownership transfer performs no remote mutation, and later removal sends the documented null clear. In-place key rotation is not supported on imported or existing mappings. Replacing an imported mapping's issuer or claim pair requires explicitly configured replacement `key_wo` and `key_wo_version`; Terraform will not destroy an imported keyless mapping when those create credentials are absent or unknown.

## Lifecycle and failure safety

Deleting the virtual key a mapping points at also deletes the mapping on LiteLLM 1.102 and later (database cascade). The next refresh reports the mapping as gone and plans a re-create, which needs `key_wo`. When a mapping's target key may be replaced, use `replace_triggered_by` on the mapping so both are re-created in one apply.


The database-generated UUID is the only resource identity. LiteLLM uniquely constrains the issuer scope plus claim pair, and the provider treats all three as immutable. A known change to the issuer or either claim requires replacement and is planned only when the complete new identity and both replacement key arguments are already known. Unknown claim values remain non-destructive until Terraform can re-plan them. Destroy-only plans never require key material. Reads remove state only for an exact HTTP 404. Delete requires a successful delete or exact 404 and then an exact-404 info read before state is discarded.

Mutations are single-attempt. The create endpoint always creates an active row; when `is_active = false`, the provider retains the confirmed UUID, sends one controlled deactivation update, validates its response, and performs a fresh info read before completing state. A failure after UUID confirmation retains UUID-only recovery state, preventing a duplicate create on the next apply.

When a create request may have committed but no canonical UUID was received, Terraform publishes no guessed identity and does not adopt by claim pair: a concurrent creator cannot be distinguished from this request. An administrator must list JWT key mappings, locate the exact issuer, claim-name, and claim-value combination, obtain its canonical UUID, and import that UUID. A later HTTP 409 can be evidence that manual recovery is required, but it is not safe identity proof.

Update failures retain prior state and private ownership. Delete endpoint 404 is not sufficient by itself: the provider still requires a singular `/info?id=...` exact-404 proof before removing state. Valid mutation responses and authoritative info reads must preserve UUID, issuer, and claim identity; malformed 2xx objects, alternate envelopes, and stale observable reads fail closed without publishing false convergence. Timestamps are observable metadata, not key-rotation proof. Sensitive request/response details are omitted from diagnostics.

`Sensitive` controls Terraform CLI/UI redaction; it is not encryption. The API returns `jwt_claim_value` as plaintext, so that plaintext is necessarily stored in Terraform state for this resource and both data sources. Protect local and remote state, backups, plans, and access to state APIs accordingly. The raw `key_wo` is different: it is write-only and is not stored in resource state or plans by the provider.
