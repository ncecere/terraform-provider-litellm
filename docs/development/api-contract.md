# LiteLLM API contract maintenance

The provider is tested against exactly LiteLLM 1.98.0. Its API review consists of:

- generated `openapi.json`, after fail-closed direct registration of all 33 lazy feature definitions in the pinned source;
- generated `internal/contract/supplemental-routes.json`, containing the complete feature/module/prefix/suffix/router-or-mount contract, per-feature live/OpenAPI counts, and the explicitly reviewed hidden route;
- generated `internal/contract/manifest.json` and `internal/contractapi/testdata/provider-operations.golden.json`;
- reviewed `internal/contract/reviewed-operation-classification.json`, with one exact normalized `METHOD /path` entry for every operation not used by the provider; and
- reviewed, non-generated `internal/contract/reviewed-pins.json`, which pins exact bytes and counts for all generated artifacts and the classification, plus category rationales and issue ownership.

The generated files and review files are development/release inputs. They are not embedded in the provider. Provider runtime remains Go-only and uses only LiteLLM's HTTP API.

## Offline verification

Run:

```sh
make contract-check
```

The verifier does not use the network or Python. It discovers `Client` transport methods by receiver and call graph, permits only the reviewed transport plumbing and wrappers, and rejects unknown `Client` HTTP abstractions, raw request construction (including in `client.go`), unresolved wrappers, methods, paths, or query names. It extracts every non-test provider call and compares method, normalized path boundaries, path parameters, and exact query names with OpenAPI plus the supplement.

Production verification also:

- verifies exact bytes for OpenAPI, supplement, manifest, provider-operation golden, and reviewed classification against `reviewed-pins.json`;
- compares extraction with both the golden and manifest, including code evidence;
- requires every OpenAPI/supplement operation to be either provider-used or classified exactly once;
- rejects stale, renamed, duplicate, overlapping, unknown-category, or unclassified operations; and
- verifies durable/non-durable disposition counts and required issue categories.

CI and the release workflow run this offline gate. Release runs it before importing signing credentials.

## Exact operation classification

`reviewed-operation-classification.json` is an exhaustive set, not a prefix rule or generated catch-all. Each entry contains an exact uppercase method, normalized path, and one category defined in `reviewed-pins.json`.

`unsupported_durable` categories are explicit Terraform resource decisions. They include credential inventory (#248), vector-store management (#249), JWT mappings (#250), configured pass-through endpoints (#251), policy versions/attachments (#252), and exact additional MCP, customer/end-user, SCIM, global configuration, and management operations (#207).

`excluded_non_durable` categories remain explicit per operation. They include inference/workload execution, operational actions, health/status, spend/analytics, cache flush, suggestion/discovery, authentication flow, testing/validation, public metadata, and observability. A new route cannot inherit one of these categories by prefix: it fails verification until its exact operation is reviewed.

## Reproducing generated artifacts

Generation requires all of the following exact inputs:

- upstream tag `v1.98.0` at commit `d8f71d7bdbd7c9873d98293f83d64c6db72847e6`;
- Python `3.12.14`;
- uv `0.12.6` (the local command rejects any other release); and
- upstream `uv.lock` SHA256 `a7cc57875c67de85bbae0f82b834f31fc9d0c029073ef29e0883787a31a985e8`.

With an existing exact checkout:

```sh
UV=/path/to/uv-0.12.6 \
LITELLM_SOURCE=../litellm \
make contract-diff
```

Without `LITELLM_SOURCE`, the update script clones the pinned commit. It exports under two different `PYTHONHASHSEED` values and byte-compares them. `contract-diff` stages and diffs all four generated artifacts, reports their staged hashes, confirms reviewed inputs were not changed, and runs full verification against the reviewed pins. The manual **Reproduce LiteLLM API contract** workflow checks out the exact source and installs exactly uv 0.12.6.

Do not substitute LiteLLM's checked OpenAPI or committed lazy OpenAPI snapshot. The exporter pins all 33 source definitions in order, including module, trigger prefixes/suffixes, router attribute, mounted application prefix, and persistent-stub flag. It rejects added, removed, duplicate, stale, unimportable, unregistrable, unextractable mounted, or zero-live-route features. It directly imports and registers each feature, disables snapshot injection, generates each feature fragment from its live routes, and requires every schema-visible feature operation to survive composition into final OpenAPI. The hidden-only `mcp_byok_oauth` feature has five live operations and an exact reviewed zero-OpenAPI exception because all five upstream routes set `include_in_schema=false`.

The source actually contains 33 `LAZY_FEATURES` definitions at the pin. Complete direct registration exposes a stale committed snapshot: reviewed OpenAPI is therefore 586 paths and 800 operations, not the former 561/772. The mounted MCP FastAPI application is extracted separately and contributes `GET /mcp/enabled`; opaque ASGI handler mounts beneath that application do not expose declarative methods for OpenAPI extraction.

## Atomic update and review process

`make contract-update` never edits reviewed pins or classifications. It:

1. creates an isolated staging tree;
2. regenerates OpenAPI and supplement twice;
3. generates the manifest and provider-operation golden in staging;
4. runs the complete production verifier in staging against the existing reviewed classification and pins;
5. copies every validated output to destination-side temporary files while retaining each destination's permissions;
6. creates destination-side backups for all four current files;
7. replaces the four generated files; and
8. removes backups and staging only after all replacements succeed.

On an ordinary command error or caught `HUP`, `INT`, or `TERM`, the installer restores every destination from its backup before exiting and removes backups and staging. Thus, after the installer returns, all four files are either the old validated set or the new validated set, never a mixed final set. The offline failure-injection test fails and interrupts after each of the four replacements, tests success, and verifies contents, permissions, and cleanup:

```sh
make contract-update-atomicity-test
```

Four independent filesystem names cannot provide a single visibility instant to concurrent readers. During replacement or rollback a reader can observe an intermediate set. An uncatchable `SIGKILL`, power loss, kernel failure, or storage failure can also stop rollback and leave an intermediate set; recovery is to restore the reviewed commit or rerun the validated update. The guarantee above is a caught-failure final-state guarantee, not a multi-file filesystem transaction.

For an intentional LiteLLM/API change:

1. Run `contract-diff` and inspect all exact route/schema/provider-call changes and reported staged hashes.
2. Manually edit `reviewed-operation-classification.json` for every added, removed, or renamed operation. Do not use prefix defaults or broad placeholder entries.
3. Manually update `reviewed-pins.json` with the reviewed classification hash/counts and the four generated hashes/counts reported by staging. This file is never generator-written.
4. Run `contract-update`; it succeeds only when staged outputs exactly match those independently reviewed pins.
5. Run `contract-diff`, `contract-check`, full test/vet/race/format/assembly checks, and confirm binary exclusion.

This deliberate two-party-style step means coordinated edits to OpenAPI, manifest, or golden still fail unless the separately reviewed pins are changed visibly.

## Source and Pydantic limitations

FastAPI OpenAPI reflects imported router objects and Pydantic models, not every executable Python branch. Lazy routers are absent until registered, mounted opaque ASGI handlers do not declare HTTP methods, and `include_in_schema=false` routes are absent by design. Pydantic aliases and dependency-injected query parameters can differ from names suggested by source. The exporter therefore walks registered FastAPI routes, including hidden routes, checks duplicate contracts, and uses bounded AST only for the reviewed hidden organization PATCH. The offline verifier checks URL-level method/path/query contracts; it does not claim request/response semantic validation beyond generated OpenAPI.

Lifecycle decisions, retries/absence, runtime escaping, and upgrade/import/client compatibility remain scoped to #202, #203, #207/#248-252, and #210.
