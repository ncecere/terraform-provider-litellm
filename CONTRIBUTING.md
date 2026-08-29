# Contributing

Thank you for contributing to the LiteLLM Terraform provider.

## Before opening a change

- Search existing issues and pull requests.
- Keep each change focused on one lifecycle or compatibility concern.
- For behavior that depends on LiteLLM, identify the exact supported upstream version and endpoint contract.
- Never include API keys, access tokens, customer identifiers, Terraform state, private logs, or local variable files.

For security vulnerabilities, follow [SECURITY.md](SECURITY.md) instead of opening a public issue.

## Development setup

Development requires Go 1.24 or newer. Terraform 1.1 or newer and OpenTofu 1.6 or newer are supported clients. The tested backend is LiteLLM 1.98.0.

```bash
git clone https://github.com/ncecere/terraform-provider-litellm.git
cd terraform-provider-litellm
go mod download
go test ./...
```

The repository's historical Go module identifier is retained for provider-build compatibility. This repository is distributed as a Terraform provider executable and does not expose a supported Go library API. Consumers should use `registry.terraform.io/ncecere/litellm`, not import the Go module.

## Validation

Run the relevant focused tests while developing, then run the complete local checks before requesting review:

```bash
go test ./... -count=1
go vet ./...
go build ./...
make contract-check
terraform fmt -check -recursive examples internal_testing
```

Changes to provider runtime behavior also require race tests, source-pinned contract reproduction, Terraform/OpenTofu assembly, and appropriate lifecycle or upgrade-matrix coverage. Destructive acceptance tests require the repository's disposable LiteLLM stack and both explicit safety confirmations documented in `internal_testing/README.md`.

Do not update generated contract artifacts or runtime evidence until the corresponding source change has been independently reviewed. Generated artifacts must reproduce from the pinned source and tooling versions.

## Pull requests

Describe:

- the user-visible behavior and compatibility impact;
- null, unknown, empty, import, update, clear, and deletion semantics;
- how uncertain mutations and malformed readback retain state;
- the exact tests and pinned-source evidence used; and
- any intentionally unsupported upstream behavior.

Preserve existing resource addresses, IDs, imports, public HCL types, and state unless an explicit migration is included and tested.
