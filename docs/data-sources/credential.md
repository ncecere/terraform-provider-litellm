# litellm_credential (Data Source)

Reads non-sensitive LiteLLM credential metadata. Credential values are never exposed.

## Lookup by name

```hcl
data "litellm_credential" "existing" {
  credential_name = "team/openai production"
}
```

This uses the exact `GET /credentials/by_name/{credential_name:path}` route. Names are safely escaped, including slash, percent, spaces, Unicode, query/fragment characters, and traversal-like text.

LiteLLM v1.98 stores the durable credential in its database but reads this route from each worker's process-local `litellm.credential_list`, so workers can temporarily disagree after a mutation. The provider samples four conclusive responses over fresh connections (with at most eight total probes when transient failures require retries). One consistent present version plus 404 responses returns that version with a worker-convergence warning; conflicting present versions fail instead of selecting one arbitrarily. Four consecutive exact 404 responses are required to report absence. Production freshness uses an isolated clone of Go's `*http.Transport`; a custom transport whose connection freshness cannot be proved fails closed. Fresh connections avoid keepalive pinning but cannot guarantee that a load balancer selects every worker, so this is bounded fail-safe sampling rather than a fixed convergence promise. Reloading or restarting workers may be required.

## Lookup by model ID

```hcl
data "litellm_credential" "deployment" {
  # Stable Terraform identity; LiteLLM synthesizes another response name.
  credential_name = "production-deployment-lookup"
  model_id        = litellm_model.production.model_id
}
```

When `model_id` is non-empty, the provider uses LiteLLM v1.98's exact `GET /credentials/by_model/{model_id}` route. It is not an ignored query parameter on the by-name route. The same bounded fresh-connection sampling is used for this credential data source. The route declares one ordinary path segment rather than a path-capable parameter, so this data source safely rejects model IDs containing `/`. This restriction applies only to the data-source route; the resource may send slash-containing `model_id` values in its JSON create body.

LiteLLM's by-model response synthesizes `credential_name`. Terraform preserves the configured `credential_name` as both `id` and the public stable value.

## Arguments

* `credential_name` - (Required) Non-empty exact name for by-name lookup and stable identity for by-model lookup.
* `model_id` - (Optional) Model deployment ID selecting the by-model route. Slash is not representable by that route.

## Attributes

* `id` - Configured stable `credential_name`.
* `credential_info` - Existing computed `map(string)` projection. Only top-level string values appear, preserving the original Terraform type.
* `credential_info_json` - Additive canonical full JSON object. It includes nested objects, arrays, booleans, nulls, and the exact number lexemes returned by LiteLLM. This does not recover pre-LiteLLM fractional precision: Python normally parses JSON fractions as binary `float`, so use strings when a decimal must remain textually exact. Python integers remain arbitrary precision.

## Security

The API response's masked `credential_values` object is discarded. Neither a legacy map nor full JSON values output is exposed by this data source.
