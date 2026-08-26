# Previous-release execution matrix

This directory contains the issue #210 execution harness. `matrix.json` is an inventory and scenario specification, not evidence that a scenario passed. Only records emitted after a scenario command completes can appear in a destructive report. Report summaries are recalculated from those records.

## Inventory

The harness compares the checked-in inventory to provider registration:

- 23 resources;
- 33 data sources;
- 23 upgrade scenarios;
- 23 import scenarios;
- 2 replacement scenarios; and
- 2 controlled-fault recovery scenarios.

Assembly validates inventory, HCL formatting, and provider schemas. Its execution-category pass counts are always zero. Enterprise, unavailable API, and pre-1.11 features are explicit skips with controlled reasons; they are never counted as passes. Local execution fails if any resource, data source, or required category lacks one result.

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

Every current-provider dev override receives a dedicated private directory containing exactly one verified executable. Execution records include only safe SHA-256 provenance and the current canonical provider-schema fingerprint.

## Assembly

```sh
mkdir -m 700 /tmp/issue210-report
MATRIX_REPORT=/tmp/issue210-report/assembly.json \
  sh internal_testing/upgrade_matrix/run.sh assembly
python3 -m unittest discover -s internal_testing/upgrade_matrix/tests -p 'test_*.py'
sh internal_testing/upgrade_matrix/tests/safety_test.sh
```

Reports are strict schema-version 2 JSON. They reject arbitrary fields and diagnostics, protected field names, secrets, credentials, UUIDs, URLs, response bodies, and filesystem paths. Diagnostic titles are mapped internally to controlled codes. Writes use a private unique temporary file, fsync, and atomic create-only linking; an existing or symlink destination fails.

## Local execution

The backend is the disposable loopback-only LiteLLM v1.98.0 Compose stack. Each command has a wall deadline, child output is bounded and private, curl operations have connect/total timeouts, and cleanup failure overrides success.

```sh
internal_testing/compose.sh up -d
mkdir -m 700 /tmp/issue210-report
TF_ACC=1 \
LITELLM_ACCEPTANCE_CONFIRM=local-v1.98.0 \
MATRIX_CLI="$HOME/.cache/terraform-provider-litellm/tools/terraform/1.11.4/$(go env GOOS)_$(go env GOARCH)/terraform" \
MATRIX_REPORT=/tmp/issue210-report/result.json \
sh internal_testing/upgrade_matrix/run.sh local
internal_testing/compose.sh down -v
```

Repository-owned non-PR workflow jobs execute this lane against a new local backend with all four pinned CLIs. Pull requests and forks run assembly only. There is no workflow-enabled remote mutation lane; adding one requires a separate future gate and reviewed scenario allowlist.

## Import ownership

Owned-object import tests use two workspaces and a cryptographically random namespace:

- the producer creates and retains target/dependency state;
- the importer receives private dependency context, removes only the target address, imports, refreshes, and proves no drift;
- importer cleanup removes only the imported target address;
- the producer destroys everything it owns; and
- a source-free re-import must fail, proving authoritative absence.

A genuine-preexisting mode may only detach imported state and must never call destroy. It is not inferred from the owned producer mode.

## Replacement and recovery

Replacement plans require exact ordered `["create", "delete"]` actions because both reviewed fixtures use distinct server identities and explicit `create_before_destroy`. Any dependency action is rejected. Apply JSON must show target create before target delete. Process-local HMAC comparisons prove target identity changed while dependency identity stayed stable; state address and dependency relation converge with no drift. Destroy must leave empty state and authoritative re-import must fail.

Recovery uses `fault_proxy.py`, a loopback-only proxy that faults one allowlisted endpoint before forwarding. Dependencies are created first. Evidence requires a target request attempt, zero target forwards/commits, exact allowlisted diagnostic mapping, no target identity/private state, successful retry, no drift, empty cleanup state, and authoritative absence. A dead base URL is not a recovery scenario.

## Upgrade comparison

The exact signed v2.0.1 executable applies unchanged HCL first. The exact current executable then performs a no-change plan and refresh-only apply. Canonical comparison covers addresses, types, schema versions, every non-sensitive semantic value, identifier equality through a process-local HMAC, and provider-private presence signals. Reviewed computed migrations must be listed per type in `matrix.json`; the default allowlist is empty. The HMAC key is created inside the comparator and is never exported to Terraform or provider children. State and HCL are not rewritten during upgrade.
