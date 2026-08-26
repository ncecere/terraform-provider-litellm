# Previous-release execution matrix

This directory contains the issue #210 execution harness. `matrix.json` is an inventory and scenario specification, not evidence that a scenario passed. `deadline.py` is the bounded command supervisor: it emits digest-only command receipts bound to the run nonce, exact CLI lane, candidate commit, provider/schema, harness, and matrix. Scenario observations must reference one of those receipts and an observed plan/state/API assertion digest. Report summaries are recalculated from the trusted ledger.

## Inventory

The harness compares the checked-in inventory to provider registration:

- 24 resources;
- 35 data sources;
- 24 upgrade scenarios;
- 24 import scenarios;
- 3 replacement scenarios; and
- 2 controlled-fault recovery scenarios; and
- 1 current-provider-only MCP immediate-import/no-drift/private-provenance assertion folded into the existing `import:litellm_mcp_server` scenario (never added as a fourth documentation scenario).

The execution matrix is exactly 166 scenarios. The Terraform 1.11.4 local lane is contractually 156 passes and 10 exact, reason-bound skips. Assembly validates inventory, HCL formatting, and provider schemas. Its execution-category pass counts are always zero. Enterprise, unavailable API, pre-1.11 features, and resources absent from signed v2.0.1 are explicit skips with controlled reasons; they are never counted as passes. Local execution fails if any resource, data source, or required category lacks one result.

## Provenance

`tools.lock.json` pins Terraform 1.0.11/1.11.4 and OpenTofu 1.6.3/1.11.1 archives. It also pins the v2.0.1 Registry metadata, release key, full fingerprint, detached signature, checksum manifest, Registry manifest, archives, and extracted executable digests.

The installer:

1. uses a mode-0700 cache and an exclusive lock;
2. rejects symlinked/non-regular/hard-linked cache entries;
3. uses unique O_EXCL partial files, bounded size, timeouts, and retries;
4. verifies Registry metadata and the exact public key digest/key ID;
5. verifies `SHA256SUMS.sig` against fingerprint `C753834A70062246C92CEF56F0A1AEC231353F8B` in a fresh GnuPG home;
6. trusts archive and manifest checksums only after signature verification;
7. extracts into a fresh private directory while rejecting links and unsafe paths; and
8. verifies the exact executable name and digest before constructing the mirror.

Every current-provider dev override receives a dedicated private directory containing exactly one verified executable. The redacted report and safe evidence ledger contain only controlled enums, bounded integers, HMAC-authenticated receipts, commit/digest provenance, and exact canonical schema fingerprints. The ephemeral ledger signing key and raw command output, plans, state, IDs, endpoints, and API assertions stay in bounded mode-0700 scratch storage. The key is securely unlinked after every cleanup attempt. Successful reports remove all raw scratch material; failed execution retains bounded diagnostics or recovery state without the session key.

## Assembly

```sh
report_root=$(python3 -c 'from pathlib import Path; print(Path("/tmp/issue210-report").resolve())')
mkdir -m 700 "$report_root"
MATRIX_REPORT="$report_root/assembly.json" \
  sh internal_testing/upgrade_matrix/run.sh assembly
python3 -m unittest discover -s internal_testing/upgrade_matrix/tests -p 'test_*.py'
sh internal_testing/upgrade_matrix/tests/safety_test.sh
```

Reports are strict schema-version 3 JSON. They reject arbitrary fields and diagnostics, protected field names, secrets, credentials, UUIDs, URLs, response bodies, and filesystem paths. Diagnostic titles are scenario-specifically mapped to controlled codes. Publication opens or creates every path component relative to retained directory FDs with `O_NOFOLLOW`, then uses an exclusive temporary file, file/directory `fsync`, and atomic create-only linking. Existing destinations, symlink ancestors, and mid-operation ancestor swaps fail or remain confined to the already opened directory.

A local execution writes both `result.json` and `result.evidence.jsonl`. The latter is the redacted, digest-only audit ledger; it is safe to preserve with the report. Data-source completion is derived from an immediately captured successful refresh-only command, the exact Terraform configuration catalog, complete refreshed state, and a final detailed-exitcode zero-drift plan. Eager reads absent from initial plan changes therefore remain execution-backed. Phase-only records, fabricated TSV, or matrix-only claims cannot publish a report.

## Local execution

The backend is the disposable loopback-only LiteLLM v1.98.0 Compose stack. Each command has a wall deadline, child output is bounded and private, curl operations have connect/total timeouts, and cleanup failure overrides success.

```sh
internal_testing/compose.sh up -d
report_root=$(python3 -c 'from pathlib import Path; print(Path("/tmp/issue210-report").resolve())')
mkdir -m 700 "$report_root"
TF_ACC=1 \
LITELLM_ACCEPTANCE_CONFIRM=local-v1.98.0 \
MATRIX_CLI="$HOME/.cache/terraform-provider-litellm/tools/terraform/1.11.4/$(go env GOOS)_$(go env GOARCH)/terraform" \
MATRIX_REPORT="$report_root/result.json" \
sh internal_testing/upgrade_matrix/run.sh local
internal_testing/compose.sh down -v
```

Repository-owned non-PR workflow jobs execute this lane against a new local backend with all four pinned CLIs. Pull requests and forks run assembly only. Tag releases run all four full lanes against the exact tagged SHA before GPG import, build, or signing; each gate job has read-only contents permission and a wall timeout. There is no workflow-enabled remote mutation lane; adding one requires a separate future gate and reviewed scenario allowlist.

## Import ownership

Owned-object import tests use two workspaces and a cryptographically random namespace:

- the producer creates and retains target/dependency state;
- the importer receives private dependency context, removes only the target address, imports, refreshes, and proves no drift;
- importer cleanup removes only the imported target address;
- the producer destroys everything it owns; and
- a source-free re-import must fail with an exact provider/API endpoint absence diagnostic (including LiteLLM v1.98's bounded 400 absence on affected info routes), never a plan/address/configuration error.

The agent import limitation is skippable only when the bounded refresh returns the exact allowlisted role-redaction diagnostic/status. Every other agent or import failure aborts the lane.

A genuine-preexisting mode may only detach imported state and must never call destroy. It is not inferred from the owned producer mode.

## Replacement and recovery

Replacement plans require exact ordered `["create", "delete"]` actions because all reviewed fixtures use distinct server identities and explicit `create_before_destroy`. Any dependency action is rejected. Apply JSON must show target create before target delete. Process-local HMAC comparisons prove target identity changed while dependency identity stayed stable; state address and dependency relation converge with no drift. Destroy must leave empty state and authoritative re-import must fail.

Recovery uses `fault_proxy.py`, a loopback-only proxy that faults one allowlisted endpoint before forwarding. Dependencies are created first. Evidence requires a target request attempt, zero target forwards/commits, exact allowlisted diagnostic mapping, no target identity/private state, successful retry, no drift, empty cleanup state, and authoritative absence. A dead base URL is not a recovery scenario.

## Upgrade comparison

The exact signed v2.0.1 executable applies unchanged HCL first. The exact current executable reviews every changed field, applies only an exact allowlisted migration update when needed, follows it with a refresh-only migration, compares canonical state, and must then produce a final detailed-exitcode zero-drift plan. Canonical comparison covers addresses, types, schema versions, every non-sensitive semantic value, identifier equality through a process-local HMAC, and provider-private presence signals. Reviewed computed migrations must be listed per type in `matrix.json`; the default allowlist is empty. A path is either a legacy top-level attribute name or an exact nested schema path. Path components use Terraform identifiers (`[A-Za-z_][A-Za-z0-9_]*`), joined by `.`, and every traversed list, set, or map must use `[*]` (for example, `member[*].user_id`). `[*]` selects collection elements, not map keys: map keys and list positions remain semantic. Paths must end at a non-sensitive computed attribute. Duplicate, overlapping, malformed, absent, non-computed, sensitive, whole-structure, and wrong-mode paths fail closed.

`litellm_team_member_add.member[*].user_id` is the sole nested exception. The v2.0.1 fixture owns a member by email, while the current provider resolves that same member to LiteLLM's canonical computed user ID during refresh. The comparator masks only that leaf before canonical set ordering, so generated IDs cannot reorder the set into a false failure; member count, email, role, and every other sibling remain mandatory exact matches. An unchanged reviewed leaf does not report a migration.

The HMAC key is created inside the comparator and is never exported to Terraform or provider children. State and HCL are not rewritten during upgrade.
