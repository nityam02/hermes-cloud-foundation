# Hermes Cloud Foundation — developer and CI entry points.
# Every target runs without cloud credentials; engine and test commands are wrapped in
# $(ISOLATE) so that credentials present on a developer machine can never be used.

SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help

GO        ?= go
TOFU      ?= tofu
TERRAFORM ?= terraform
CONFTEST  ?= conftest

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

export TF_PLUGIN_CACHE_DIR ?= $(HOME)/.cache/hermes-tf-plugins
export TF_IN_AUTOMATION := 1
export TF_INPUT := 0

# Credential isolation for anything that could talk to a cloud.
ISOLATE := env AWS_CONFIG_FILE=/dev/null AWS_SHARED_CREDENTIALS_FILE=/dev/null AWS_PROFILE= \
	AWS_ACCESS_KEY_ID= AWS_SECRET_ACCESS_KEY= AWS_SESSION_TOKEN= AWS_EC2_METADATA_DISABLED=true \
	GOOGLE_APPLICATION_CREDENTIALS=/dev/null GOOGLE_OAUTH_ACCESS_TOKEN= CLOUDSDK_CONFIG=/dev/null \
	ALIBABA_CLOUD_CREDENTIALS_FILE=/dev/null ALIBABA_CLOUD_ACCESS_KEY_ID= ALIBABA_CLOUD_ACCESS_KEY_SECRET= \
	ALICLOUD_ACCESS_KEY= ALICLOUD_SECRET_KEY=

# Module directories (any directory under infra/ holding .tf files) and directories with tofu tests.
TF_MODULE_DIRS := $(shell find infra -name '*.tf' -not -path '*/.terraform/*' -exec dirname {} \; 2>/dev/null | sort -u)
TF_TEST_DIRS   := $(shell find infra -name '*.tftest.hcl' -not -path '*/.terraform/*' 2>/dev/null | xargs -n1 dirname 2>/dev/null | sed 's|/tests$$||' | sort -u)
COMPOSITIONS   := infra/providers/aws/platform infra/providers/gcp/platform infra/providers/alibaba/platform

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

## ---------------------------------------------------------------- Go

.PHONY: build
build: ## Build bin/hermes
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o bin/hermes ./cmd/hermes

.PHONY: test
test: ## Run Go unit tests (no network, no credentials)
	$(ISOLATE) $(GO) test -count=1 ./...

.PHONY: test-race
test-race: ## Run Go unit tests with the race detector
	$(ISOLATE) $(GO) test -race -count=1 ./...

.PHONY: cover
cover: ## Go test coverage report (coverage.out)
	$(ISOLATE) $(GO) test -count=1 -coverprofile=coverage.out ./... && $(GO) tool cover -func=coverage.out | tail -1

.PHONY: lint
lint: ## gofmt check, go vet, golangci-lint (if installed)
	@out=$$(gofmt -l $$(git ls-files '*.go' 2>/dev/null || find . -name '*.go' -not -path './.hermes/*')); \
	  if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	$(GO) vet ./...
	@if command -v golangci-lint >/dev/null; then golangci-lint run ./...; else echo "golangci-lint not installed; skipped"; fi

.PHONY: fmt
fmt: ## Format Go and HCL
	gofmt -w $$(find . -name '*.go' -not -path './.hermes/*')
	$(TOFU) fmt -recursive infra
	$(TOFU) fmt -recursive tests

## ---------------------------------------------------------------- OpenTofu / Terraform

.PHONY: tf-fmt-check
tf-fmt-check: ## tofu fmt -check on all HCL
	$(TOFU) fmt -check -recursive infra
	$(TOFU) fmt -check -recursive tests

.PHONY: tf-validate
tf-validate: tf-fmt-check ## init -backend=false + validate every module (downloads providers; no credentials)
	@mkdir -p "$(TF_PLUGIN_CACHE_DIR)"
	@for d in $(TF_MODULE_DIRS); do \
	  echo "==> validate $$d"; \
	  $(ISOLATE) $(TOFU) -chdir=$$d init -backend=false -input=false -no-color >/dev/null || exit 1; \
	  $(ISOLATE) $(TOFU) -chdir=$$d validate -no-color || exit 1; \
	done

.PHONY: tf-test
tf-test: ## tofu test with mock providers for every module that has tests
	@mkdir -p "$(TF_PLUGIN_CACHE_DIR)"
	@for d in $(TF_TEST_DIRS); do \
	  echo "==> test $$d"; \
	  $(ISOLATE) $(TOFU) -chdir=$$d init -backend=false -input=false -no-color >/dev/null || exit 1; \
	  $(ISOLATE) $(TOFU) -chdir=$$d test -no-color || exit 1; \
	done

.PHONY: tf-compat
tf-compat: ## Validate and test the provider compositions with Terraform as well
	@for d in $(COMPOSITIONS); do \
	  echo "==> terraform $$d"; \
	  $(ISOLATE) $(TERRAFORM) -chdir=$$d init -backend=false -input=false -no-color >/dev/null || exit 1; \
	  $(ISOLATE) $(TERRAFORM) -chdir=$$d validate -no-color || exit 1; \
	  $(ISOLATE) $(TERRAFORM) -chdir=$$d test -no-color || exit 1; \
	done

.PHONY: contract-test
contract-test: ## Static contract checks + shared contract runs on every composition (mock providers)
	$(GO) test -count=1 ./tests/contracts/...
	@for d in $(COMPOSITIONS); do \
	  echo "==> contract runs $$d"; \
	  $(ISOLATE) $(TOFU) -chdir=$$d init -backend=false -input=false -no-color >/dev/null || exit 1; \
	  $(ISOLATE) $(TOFU) -chdir=$$d test -no-color -filter=tests/contract.tftest.hcl || exit 1; \
	done

.PHONY: integration
integration: build ## Render every example and validate the rendered roots with the engine (no credentials)
	$(ISOLATE) HERMES_TF_INTEGRATION=1 $(GO) test -count=1 -tags integration ./...

## ---------------------------------------------------------------- Policy, security, docs

.PHONY: ui-install
ui-install: ## Install the Hermes UI dependencies (lockfile, no install scripts)
	cd ui && npm ci --ignore-scripts --no-audit --no-fund

.PHONY: ui-test
ui-test: ## Typecheck and unit-test the Hermes UI
	cd ui && npm run typecheck && npm test

.PHONY: ui-build
ui-build: ## Build the Hermes UI and place it where the CLI embeds it (then run make build)
	cd ui && npm run build
	mkdir -p internal/uiserver/dist && touch internal/uiserver/dist/.gitkeep
	find internal/uiserver/dist -mindepth 1 ! -name .gitkeep -delete
	cp -R ui/dist/. internal/uiserver/dist/

.PHONY: policy-test
policy-test: ## conftest verify for the plan policies
	$(CONFTEST) verify --policy policies/plan --policy policies/tests --data policies/tests --no-color

.PHONY: policy-fixtures
policy-fixtures: ## Regenerate policies/tests/fixtures/*_default from mock-provider plans of the compositions
	TOFU=$(TOFU) ./scripts/gen-policy-fixtures.sh

.PHONY: policy-fixtures-check
policy-fixtures-check: ## Fail when the composition-derived policy fixtures are stale (module/policy drift)
	TOFU=$(TOFU) ./scripts/gen-policy-fixtures.sh --check

.PHONY: security
security: ## gitleaks, trivy config, checkov, tflint, conftest (see scripts/security-scan.sh)
	./scripts/security-scan.sh

.PHONY: forbidden-terms
forbidden-terms: ## Check the tree against a private denylist: make forbidden-terms TERMS=/path/to/terms.txt
	./scripts/check-forbidden-terms.sh "$(TERMS)"

.PHONY: docs-links
docs-links: ## Check links in Markdown (offline: local files and anchors only)
	lychee --offline --no-progress --include-fragments './**/*.md'

.PHONY: docs-links-online
docs-links-online: ## Check every link in Markdown, including external URLs
	lychee --no-progress './**/*.md'

.PHONY: docs-generate
docs-generate: build ## Regenerate generated documentation
	./bin/hermes migrate catalog --format markdown > docs/migration/service-catalog.md

## ---------------------------------------------------------------- Demos

.PHONY: demo-discovery
demo-discovery: build ## AWS discovery demo against a local moto server (no real AWS); VENV=<path to a moto venv> optional
	VENV="$(VENV)" ./examples/migration/aws-discovery-demo/run-demo.sh

## ---------------------------------------------------------------- Aggregates

.PHONY: ci
ci: lint test contract-test tf-validate tf-test policy-test docs-links ## Everything CI runs (except security, tf-compat and demos)

.PHONY: clean
clean: ## Remove build outputs, working dirs and local .terraform directories
	rm -rf bin .hermes coverage.out coverage.html
	find infra tests -name .terraform -type d -prune -exec rm -rf {} +
	find infra tests -name .terraform.lock.hcl -delete
