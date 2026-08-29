HOSTNAME ?= registry.terraform.io
NAMESPACE ?= ncecere
NAME ?= litellm
VERSION ?= 2.1.0
OS_ARCH ?= $(shell go env GOOS)_$(shell go env GOARCH)

default: install

build:
	go build -o terraform-provider-${NAME}

install: build
	mkdir -p ~/.terraform.d/plugins/${HOSTNAME}/${NAMESPACE}/${NAME}/${VERSION}/${OS_ARCH}
	mv terraform-provider-${NAME} ~/.terraform.d/plugins/${HOSTNAME}/${NAMESPACE}/${NAME}/${VERSION}/${OS_ARCH}/terraform-provider-${NAME}_v${VERSION}

test:
	go test ./...

coverage:
	go test -covermode=atomic -coverprofile=coverage.out ./...
	@printf 'Total coverage: %s\n' "$$(go tool cover -func=coverage.out | tail -1 | awk '{print $$NF}')"
	@echo "HTML report: go tool cover -html=coverage.out"

fmt:
	go fmt ./...

vet:
	go vet ./...

# Offline: verifies checked artifacts, reviewed inventory, and every provider HTTP call.
contract-check:
	go run ./internal/cmd/contract-check
	sh tools/litellm-contract/check-binary.sh

# Networked unless LITELLM_SOURCE points at an existing exact upstream checkout.
contract-update:
	sh tools/litellm-contract/update.sh update

# Reproduce without modifying checked files and show any generated contract drift.
contract-diff:
	sh tools/litellm-contract/update.sh diff

# Offline failure/signal injection plus exclusive-writer interleavings and stale-lock refusal.
contract-update-atomicity-test:
	sh tools/litellm-contract/test-failure-atomicity.sh

lint:
	golangci-lint run

clean:
	rm -f terraform-provider-${NAME}
	rm -rf ~/.terraform.d/plugins/${HOSTNAME}/${NAMESPACE}/${NAME}/${VERSION}

# Start LiteLLM + DB for local/smoke testing. Run once before make smoke.
local:
	@sh internal_testing/compose.sh up -d
	@echo "Run make logs to follow LiteLLM logs, then make smoke resources=... or datasources=..."

# Follow LiteLLM proxy logs (run after make local).
logs:
	@sh internal_testing/compose.sh logs -f litellm

# Smoke test: selected files run together in an isolated plan/apply/no-drift/destroy workspace.
# Requires: make local (LiteLLM + DB up), make build. At least one of resources= or datasources= is required (comma-separated).
# Usage:
#   make smoke resources=model_minimal.tf
#   make smoke resources=model_minimal.tf,key_minimal.tf
#   make smoke datasources=keys_list.tf
#   make smoke resources=model_minimal.tf datasources=keys_list.tf
# CURDIR is Make's current working directory (repo root); passed so the script finds internal_testing and the provider binary.
smoke: build
	@test -f terraform-provider-$(NAME) || (echo "Run 'make build' first."; exit 1)
	@test -n "$(resources)$(datasources)" || (echo "Usage: make smoke resources=file.tf [datasources=file.tf]"; exit 1)
	@sh internal_testing/smoke.sh "$(CURDIR)" resources $(strip $(subst ,, ,$(resources))) datasources $(strip $(subst ,, ,$(datasources)))

# Destructive local acceptance matrix. Start the pinned disposable Compose stack first.
# Usage: TF_ACC=1 LITELLM_ACCEPTANCE_CONFIRM=local-v1.98.0 make testacc
testacc: build
	@sh internal_testing/acceptance.sh

# Non-destructive previous-release/import matrix assembly and safety tests.
upgrade-matrix-assembly:
	@sh internal_testing/upgrade_matrix/run.sh assembly

upgrade-matrix-test:
	@python3 -m unittest discover -s internal_testing/upgrade_matrix/tests -p 'test_*.py'
	@sh internal_testing/upgrade_matrix/tests/safety_test.sh

# Destructive pinned local lane; requires the same explicit double opt-in.
upgrade-matrix: build
	@sh internal_testing/upgrade_matrix/run.sh local

.PHONY: build install test coverage fmt vet contract-check contract-update contract-diff contract-update-atomicity-test lint clean local logs smoke testacc upgrade-matrix-assembly upgrade-matrix-test upgrade-matrix
