# LiteLLM API contract maintenance

The provider is tested against exactly LiteLLM 1.98.0. Its reviewed management API contract consists of:

- `openapi.json`, generated from the exact upstream tag and commit after fail-closed registration of every lazy router used by the provider;
- `internal/contract/supplemental-routes.json`, containing only explicitly reviewed hidden routes; and
- `internal/contract/manifest.json`, which pins provenance, checksums, counts, provider call evidence, and the reviewed unsupported inventory.

These are development and release-check artifacts. They are not embedded in the provider binary. Provider runtime behavior remains Go-only and uses the LiteLLM HTTP API; Python and an upstream source checkout are never runtime dependencies.

## Offline verification

Run:

```sh
make contract-check
```

The verifier does not use the network or import Python. It parses non-test Go files under `internal/provider`, resolves the approved HTTP wrappers and URL helpers, and rejects unresolved HTTP calls or raw request construction. It then checks every extracted method, normalized path, path parameter, and query name against the checked OpenAPI and supplemental artifacts. Checksums, reviewed counts, operation evidence, stale manifest entries, and the unsupported inventory are also enforced.

CI and the release workflow run only this offline check.

## Reproducing or updating

An update requires the exact upstream source and dependencies resolved from its pinned `uv.lock`. With an existing checkout:

```sh
make contract-update LITELLM_SOURCE=../litellm
```

Without `LITELLM_SOURCE`, the target clones the pinned upstream commit. It verifies that commit and its `v1.98.0` tag, verifies the lock checksum, creates the pinned Python 3.12.14/uv environment, exports with two different `PYTHONHASHSEED` values, and byte-compares both results before replacing checked files. `make contract-diff` performs the same networked reproduction but only compares generated output with the repository.

The manual **Reproduce LiteLLM API contract** workflow independently checks out the exact upstream commit and uses pinned Python and uv versions. It regenerates twice and fails on any diff.

Do not copy LiteLLM's checked OpenAPI file as a fallback. LiteLLM lazily imports some routers and its normal loader warns and skips failed optional imports. The exporter instead fails closed if a required provider router cannot import, register, or appear in the schema. A failed update must leave the prior reviewed artifacts intact.

## Upgrade review

A LiteLLM upgrade is a deliberate API review, separate from the client/import matrix tracked in #210:

1. Select an immutable upstream tag and commit and review its Python requirement and `uv.lock`.
2. Update the pins in `tools/litellm-contract/export.py`, the manual workflow, and the manifest.
3. Review the lazy-feature list and the single bounded hidden-route AST extraction. Do not broaden AST inference to manufacture an OpenAPI substitute.
4. Run `make contract-update`, inspect the OpenAPI and operation inventory diffs, and update the unsupported durable-operation review.
5. Run `make contract-check`, the complete Go test/vet/race suite, formatting checks, and release snapshot checks.
6. Confirm the provider binary still contains no Python, LiteLLM source, OpenAPI, supplemental, or manifest payload.

Route presence alone does not establish a safe Terraform lifecycle. Resource decisions, retries/absence, runtime escaping, and upgrade/import/client compatibility remain in #202, #203, #207/#248-252, and #210 respectively.

## Source and Pydantic limitations

FastAPI's OpenAPI output reflects imported router objects and Pydantic models, not every executable Python branch. Routes marked `include_in_schema=false` are absent by design, and lazy routers are absent until registered. Pydantic aliases and dependency-injected query parameters can also differ from names suggested by function source. For that reason, the exporter walks registered FastAPI routes (including hidden routes), checks duplicate contracts, and uses bounded AST only for the explicitly reviewed hidden organization PATCH. The offline verifier compares URL-level method/path/query contracts; it does not claim to validate request or response semantics beyond the generated OpenAPI.

## Reviewed exclusions

The manifest keeps durable but unsupported groups visible: credential and vector inventories, JWT mappings, pass-through configuration, policy versions and attachments, MCP toolsets, customer/end-user records, SCIM, and global configuration surfaces. Operational actions, health, spend/analytics, cache flushes, suggestions, and inference are explicitly classified as non-durable rather than Terraform resource candidates.
