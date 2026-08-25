# litellm_credential (Resource)

Manages a LiteLLM credential while preserving the provider's original string-map interface. Terraform owns only configured keys, recursively. API- or operator-added keys are not silently adopted.

## Compatibility and heterogeneous values

`credential_info` and `credential_values` remain `map(string)`. Existing references, outputs, and state therefore keep their original Terraform types. There is no state-version migration. `credential_values` is now optional, an additive loosening that permits model-only configuration and source-free metadata-only imports.

LiteLLM v1.98 also accepts nested objects, arrays, booleans, nulls, and JSON numbers. Use the additive JSON-string attributes for those values:

```hcl
resource "litellm_credential" "full" {
  credential_name = "azure-production"

  credential_info = {
    provider = "azure"
  }
  credential_info_json = jsonencode({
    provider    = "azure" # Equal overlap is allowed.
    enabled     = true
    retry_count = 3
    labels      = ["production", "priority"]
  })

  credential_values_json = jsonencode({
    api_key = var.azure_api_key
    oauth = {
      client_id     = var.azure_client_id
      client_secret = var.azure_client_secret
    }
  })
}
```

The JSON strings are validated as non-null objects and stored in deterministic compact form. The provider preserves each configured JSON number lexeme while validating, merging, and encoding it instead of first converting it through Go `float64`.

That guarantee is provider-side, not arbitrary-precision end to end. LiteLLM is a Python service: JSON integers are decoded as arbitrary-precision Python integers, but fractional and exponent values normally become binary Python `float` values and may be rounded before LiteLLM stores or returns them. Use string values for decimal identifiers or fractions that must remain textually exact. Authoritative read-back can reject a mutation when LiteLLM's rounded fractional value no longer equals the configured JSON number.

Legacy and JSON objects are merged recursively by key ownership. Disjoint keys are combined. An overlapping key is accepted only when both surfaces encode exactly the same value; conflicting overlap fails before any request. To migrate an existing key without changing its Terraform type:

1. Add the same key/value to the JSON attribute.
2. Apply while both surfaces agree.
3. Remove the legacy copy, leaving the JSON copy as owner.

This is a configuration migration between additive attributes, not a Terraform type or state migration.

## Create sources

LiteLLM accepts these create forms:

### Values

```hcl
resource "litellm_credential" "values" {
  credential_name = "provider-values"
  credential_values = {
    api_key = var.provider_api_key
  }
}
```

When `model_id` is omitted, the merged legacy and JSON values object must be non-empty. LiteLLM v1.98 tests this object for truthiness and rejects a values-only `{}`. Omitted values, `credential_values = {}`, `credential_values_json = "{}"`, or any combination that still merges to an empty object therefore fails during planning rather than making a known-invalid request.

### Model-derived values

```hcl
resource "litellm_credential" "from_model" {
  credential_name = "copied-deployment-values"
  model_id        = litellm_model.production.model_id
}
```

`model_id` is a create-only body field and may contain `/`. It is never sent on PATCH. Any known or unknown `model_id` change uses the unconditional replacement plan modifier.

### Both fields

Configurations that set `model_id` together with omitted, empty, or non-empty legacy/JSON values are accepted. LiteLLM gives `model_id` precedence and replaces any supplied values with model-derived values. When values are configured, Terraform emits a warning, sets `credential_values_active = false`, and sets `credential_source = "model_id"`; configured compatibility values remain in state but are explicitly inactive and are not verified or claimed as applied.

When values are the active source, `credential_values_active` is `true` and `credential_source` is `"credential_values"`.

## Arguments

* `credential_name` - (Required, Forces replacement) Exact credential name. It must be non-empty. LiteLLM can accept an empty POST name, but the resulting object cannot be addressed safely for refresh or deletion. Names are escaped exactly, including `/`, `%`, spaces, Unicode, `?`, `#`, and traversal-like text.
* `credential_values` - (Optional, Sensitive) Original `map(string)` values surface. With no `model_id`, its merge with `credential_values_json` must be non-empty.
* `credential_info` - (Optional, Computed) Original `map(string)` metadata surface.
* `credential_values_json` - (Optional, Computed, Sensitive) Canonical heterogeneous JSON object.
* `credential_info_json` - (Optional, Computed) Canonical heterogeneous JSON object.
* `model_id` - (Optional, Forces replacement) Create-only model deployment source.

## Read and update safety

LiteLLM masks sensitive response leaves. The provider restores a prior owned value only when the returned mask exactly matches that same value. A mask is never adopted as a real secret.

PATCH in LiteLLM v1.98 shallow-merges the two top-level dictionaries:

* Configured leaves are owned recursively.
* Unmanaged nested siblings are hydrated from the authoritative preflight GET before a containing object is patched.
* Readable unmanaged top-level `credential_info` is also carried through PATCH because LiteLLM v1.98 can rebuild that dictionary before applying its shallow update; carrying it does not adopt it into Terraform ownership.
* If an unmanaged nested sibling is masked and cannot be reconstructed, PATCH fails before mutation.
* Planned object/scalar transitions that could discard unmanaged children fail before mutation.
* Out-of-band atomic-to-object or object-to-atomic shape drift fails guarded projection/read, is never treated as fully owned, and blocks replacement deletion rather than adopting nested children.
* Nested owned-key removal is verified after PATCH.
* Omitted top-level keys are not cleared by PATCH. JSON null is stored as null; it is not a consumed clear.

Because top-level removal cannot be proved safe, the provider reports a plan error instead of deleting and recreating the credential. Create-only replacement is allowed only when private ownership metadata and an authoritative delete preflight prove every remote key is Terraform-owned and reconstructable. This prevents replacement from destroying operator-added secrets.

PATCH and DELETE response bodies must contain LiteLLM's explicit `success: true` result. This matters because affected handlers can serialize an exception with HTTP 200. Every mutation also receives an authoritative exact-name postflight GET: updates verify owned values, masks, and removals; deletes verify exact 404 absence.

Create first proves that the exact name is absent and refuses to overwrite or adopt a collision. Unusable HTTP success, dispatched transport failures, request timeouts, and server errors receive a bounded exact-name recovery window. Because an identical concurrent create cannot be distinguished from the provider's commit, every ambiguous outcome retains only caller-known partial state plus an uncertain-ownership private marker—even when exact configuration appears during recovery. That marker blocks refresh adoption, update, replacement, and deletion until an operator verifies ownership and imports the object or deliberately removes retained state.

## Import

Import uses only the exact credential name:

```shell
terraform import litellm_credential.example 'team/openai credential%prod'
```

Import is deliberately metadata-only:

* Readable metadata populates `credential_info` and `credential_info_json` as computed output.
* Masked `credential_values` are never adopted as owned state.
* `model_id` remains null because stored credentials do not persist their create source.
* `credential_source` is `"imported"`, and `credential_values_active` is false.
* Omitted source configuration does not request replacement.

Adding a values or model source after import is rejected when remote ownership/reconstructability is unknown. This is safer than deleting or overwriting an imported credential. Create a separately named managed credential when ownership cannot be proven.

## Security

Both values attributes are sensitive, but Terraform state still stores active configured secrets. Use an encrypted, access-controlled state backend. Provider diagnostics deliberately omit names, paths, keys, request values, and response bodies where they could expose credential material.
