# Previous-release upgrade and import matrix

This directory is the fail-closed validation harness for provider issue #210. It inventories every registered provider type and reports **resource coverage separately** from upgrade, lifecycle, import, drift, replacement, failure-recovery, data-source, and documentation scenarios.

## Coverage contract

| Category | Count | Notes |
|---|---:|---|
| Registered resources | 23 | Exact comparison with provider registration and resource docs |
| v2.0.1 upgrades | 23 | Same canonical provider address, HCL, state, resource address/type, schema, and import identity |
| Lifecycle | 23 | Project runs only in the licensed lane; action resources are reported explicitly |
| Imports | 23 | Import, refresh, no-drift, then `terraform state rm`; imported objects are never destroyed |
| Drift | 23 | Refresh/no-drift and out-of-band inventory contract |
| Intentional replacements | 2 | Unsupported model clear and immutable team-member identity |
| Failed-create recovery | 2 | Model and team-member retry/cleanup |
| Data sources | 33 | Single and list inventories, with expected limitations accounted for |
| Documentation | 3 | Registry examples, import docs, and release metadata |

`matrix.json` is authoritative and machine-readable. Assembly fails if its 23/33 inventories differ from the provider, a fixture or import section is missing, a scenario count changes, or a skip has no allowlisted reason.

The two action resources are `litellm_key_block` and `litellm_team_block`. They are importable but destructive and are never pointed at non-disposable objects. `litellm_project`, `litellm_project` data, and `litellm_projects` inventory are Enterprise-only. Agent/prompt inventories may be deferred only when the pinned API lacks the inventory endpoint. Key regeneration is deliberately not attempted: LiteLLM cannot safely return a replacement secret. These are expected limitations, not silent skips.

## Reproducible tools and previous provider

`tools.lock.json` pins archive checksums for:

- Terraform 1.0.11 and 1.11.4;
- OpenTofu 1.6.3 and 1.11.1;
- published provider v2.0.1 archives, its checksum file, and Registry manifest.

Both products therefore have their required baseline and a `>=1.11` lane. The optional `key_wo` and `send_invite_email` scenarios are selected only when `supports_optional_111` succeeds. Tool installation is cache-local and atomic:

```sh
python3 internal_testing/upgrade_matrix/harness.py install-tool terraform 1.0.11
python3 internal_testing/upgrade_matrix/harness.py install-tool opentofu 1.6.3
python3 internal_testing/upgrade_matrix/harness.py install-previous
```

Pass `--offline` to require an already verified cache entry. A missing or mismatched archive fails; the harness never falls back to a PATH version or builds v2.0.1 source. The previous-provider installer verifies the known checksum-file digest, selected archive digest, archive entry in `SHA256SUMS`, and exact protocol manifest before constructing a filesystem mirror. During upgrade, unchanged HCL remains pinned to canonical `registry.terraform.io/ncecere/litellm` v2.0.1 while a CLI `dev_overrides` entry selects the current binary. A detailed-exitcode plan and HMAC-only state contract prove no replacement, address/type/schema change, or remote-ID change.

## Assembly (safe default)

Assembly performs no API or backend request and needs no credentials:

```sh
sh internal_testing/upgrade_matrix/run.sh assembly
python3 -m unittest discover -s internal_testing/upgrade_matrix/tests -p 'test_*.py'
```

If a current provider binary exists, each resource fixture is also validated through a private `dev_overrides` directory. Otherwise assembly still enforces inventories, import documentation, release metadata, Terraform formatting, checksums, feature gates, skip accounting, and fixture presence. Set `MATRIX_REPORT` to choose the JSON result location. Reports contain controlled scenario labels, statuses, skip reasons, counts, tool versions, and diagnostic **titles only**—never diagnostic bodies.

Registry examples and documentation HCL are format checked during assembly. Provider-backed fixture validation is offline. The destructive matrix additionally plans the same assembled configurations with both CLI families.

## Destructive local lane

The exact LiteLLM image remains `docker.litellm.ai/berriai/litellm:v1.98.0`, bound to loopback by `docker-compose.yml`. Compose project and database volume names are checkout-specific. Start a new isolated stack and build the current provider, then provide both confirmations:

```sh
internal_testing/compose.sh up -d
make build
TF_ACC=1 \
LITELLM_ACCEPTANCE_CONFIRM=local-v1.98.0 \
MATRIX_CLI="$HOME/.cache/terraform-provider-litellm/tools/terraform/1.0.11/$(go env GOOS)_$(go env GOARCH)/terraform" \
sh internal_testing/upgrade_matrix/run.sh local
```

The runner verifies the loopback target and reported 1.98.0 version again. It captures all child output in a mode-0700 temporary directory, limits durable output to the summary, removes scratch data on every signal, and treats cleanup errors as test failures. Upgrade-created fixtures may be destroyed because the harness owns them. Import fixtures use only state detach for cleanup. Never replace those commands with `destroy`.

Run the four pinned CLI versions separately to produce the release matrix. Do not run destructive lanes as part of this change review.

## Remote development lane

The approved remote deployment has a separate gate and is never inferred from local confirmation. It requires all of:

```sh
TF_ACC=1
LITELLM_REMOTE_ACCEPTANCE_CONFIRM=dev-disposable-objects-only
LITELLM_TEST_NAMESPACE=issue210-<8-to-32-lowercase-letters-or-digits>
LITELLM_API_BASE=https://dev.api.ai.it.ufl.edu
LITELLM_API_KEY=<credential>
```

Only a manually dispatched, repository-owned workflow may use this lane. Remote fixtures must include the unique namespace, prove exact-name absence before create, mutate only objects created in that run, and delete only those recorded owned objects. Pre-existing import inventories are read/refreshed and detached from state; they are never destroyed or updated. The checked-in runner currently stops after the remote safety preflight and offline assembly even when manually dispatched; remote mutation remains disabled until a reviewed scenario allowlist is added.

## Replacement and recovery evidence

Replacement fixtures are disposable. The runner records only HMAC comparisons of old/new LiteLLM IDs; the values themselves never appear in output. Evidence requires:

1. stable Terraform address;
2. different old/new remote identity;
3. the documented ordering (`create_before_destroy` for the model and destination-before-source-removal for membership);
4. dependent-resource convergence;
5. a post-replacement detailed-exitcode 0 plan;
6. retry after failed create; and
7. successful cleanup.

A cleanup error overrides scenario success. The tests exercise private ID comparison and cleanup-failure behavior without using real IDs.

## Data handling

- `umask 077`, mode-0700 scratch directories, bounded child output, and cleanup traps are mandatory.
- Terraform/OpenTofu stdout and stderr, state, plans, logs, IDs, URLs, and secrets stay in scratch space and are never uploaded.
- JSON artifacts are allowlisted and scanned for credential, URL, and labeled-value patterns.
- Only diagnostic titles may be reported.
- Ambient `TF_LOG`, CLI flags, and provider variables are cleared before execution.
- `dev.env` remains ignored and is never read or copied.
- Import cleanup is `state rm` only. An unexplained skip or cleanup failure fails the run.

CI runs assembly and unit/shell safety tests only. Destructive jobs use `workflow_dispatch`, an environment gate, explicit confirmations, and a same-repository condition, so forks and pull requests cannot trigger them.
