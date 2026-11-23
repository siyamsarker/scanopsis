# Copyright 2026 Siyam Sarker
# SPDX-License-Identifier: Apache-2.0

# Pinned tool versions. Tools run through `go run`, so nothing is installed
# globally and every contributor uses the same version.
GOLANGCI_LINT := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0
GOVULNCHECK   := go run golang.org/x/vuln/cmd/govulncheck@v1.10.0
SQLC          := go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1
GO_LICENSES   := go run github.com/google/go-licenses/v2@v2.0.1

# Licence policy: only these licences may appear in distributed dependencies.
# go-licenses takes a comma-separated list, license-checker a semicolon-separated one.
# 0BSD (public-domain equivalent, no attribution required) is used by tslib.
GO_LICENSES_ALLOWED  := Apache-2.0,MIT,BSD-2-Clause,BSD-3-Clause,ISC
NPM_LICENSES_ALLOWED := Apache-2.0;MIT;BSD-2-Clause;BSD-3-Clause;ISC;0BSD;OFL-1.1

# Restrict VCS-derived values to a safe character set: tag names may contain
# shell metacharacters, and these values are spliced into command lines.
SAFE    := tr -cd 'A-Za-z0-9._+-'
VERSION ?= $(shell (git describe --tags --always --dirty 2>/dev/null || echo dev) | $(SAFE))
COMMIT  ?= $(shell (git rev-parse --short HEAD 2>/dev/null || echo unknown) | $(SAFE))
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)
IMAGE   ?= scanopsis:dev

.PHONY: generate generate-check test-integration help check fmt fmt-check lint test vuln build web-install web-lint web-test web-audit web-build web image image-scan clean headers test-scripts licenses dco

help: ## List available targets
	@grep -E '^[a-z0-9-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-12s %s\n", $$1, $$2}'

check: fmt-check lint test vuln web-lint web-test build headers test-scripts licenses web-audit ## Run the fast checks (not coverage, perf, e2e or the image scan)

fmt: ## Format Go code
	$(GOLANGCI_LINT) fmt

fmt-check: ## Fail if Go code is not formatted
	$(GOLANGCI_LINT) fmt --diff

lint: ## Lint Go code
	$(GOLANGCI_LINT) run

test: ## Run Go tests with the race detector
	go test -race -count=1 ./...

test-integration: ## Run Go integration tests against PostgreSQL in Docker
	go test -race -count=1 -tags integration ./...

generate: web-install ## Regenerate Go code from SQL queries, and web API types and docs/api.md from the OpenAPI document
	cd internal/store && $(SQLC) generate
	cd web && npm run generate
	go run ./scripts/apidocs > docs/api.md

generate-check: generate ## Fail if generated code or docs/api.md is out of date
	git diff --exit-code -- internal/store web/src/api docs/api.md

vuln: ## Check Go dependencies for known vulnerabilities
	$(GOVULNCHECK) ./...

build: web-build ## Build bin/scanopsis with the web interface embedded
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/scanopsis ./cmd/scanopsis

web-install: ## Install web dependencies exactly as locked
	cd web && npm ci --ignore-scripts

web-lint: web-install ## Lint and type-check the web interface
	cd web && npm run lint

web-test: web-install ## Run web unit tests
	cd web && npm test

web-audit: web-install ## Audit the runtime npm dependencies (needs network access)
	cd web && npm audit --omit=dev --audit-level=high

web-build: web-install ## Build the web interface into web/dist/app
	cd web && npm run build

web: web-build ## Alias for web-build

image: ## Build the container image
	docker build --build-arg VERSION='$(VERSION)' --build-arg COMMIT='$(COMMIT)' -t '$(IMAGE)' .

# Two Trivy runs: the severity filter applies to secrets too, and every secret
# finding must fail, whatever its severity.
image-scan: image ## Build the image and fail on a HIGH or CRITICAL vulnerability or any secret (needs trivy)
	trivy image --scanners vuln --severity HIGH,CRITICAL --exit-code 1 '$(IMAGE)'
	trivy image --scanners secret --exit-code 1 '$(IMAGE)'

headers: ## Fail if a source file lacks the SPDX licence header
	bash scripts/check-headers.sh

test-scripts: ## Test the helper scripts in scripts/
	bash scripts/test-scripts.sh

licenses: web-install ## Enforce the licence policy for Go and runtime npm dependencies
	$(GO_LICENSES) check ./... --allowed_licenses='$(GO_LICENSES_ALLOWED)'
	cd web && npm exec --no -- license-checker-rseidelsohn --production --excludePrivatePackages --onlyAllow '$(NPM_LICENSES_ALLOWED)'

# DCO_BASE is the commit the checked range starts after.
DCO_BASE ?= origin/main
dco: ## Fail if a commit after DCO_BASE (default origin/main) lacks a matching Signed-off-by
	bash scripts/check-dco.sh '$(DCO_BASE)' HEAD

clean: ## Remove build output
	rm -rf bin web/dist/app

.PHONY: e2e e2e-down

e2e: ## Run the end-to-end tests in a browser against a Docker Compose stack
	cd test/e2e && npm ci --ignore-scripts
	cd test/e2e && npx playwright install chromium
	cd test/e2e && npx playwright test

e2e-down: ## Stop the end-to-end stack that E2E_KEEP=1 left running
	docker compose -p scanopsis-e2e -f test/e2e/compose.yaml down -v --remove-orphans

.PHONY: coverage go-coverage web-coverage

# Go coverage is measured once, across unit and integration tests, over every
# internal and cmd package. The profile lands in coverage/, which is ignored.
COVER_PROFILE := coverage/go.out

coverage: go-coverage web-coverage ## Enforce the coverage thresholds for Go and the web interface (needs Docker)

go-coverage: ## Measure Go coverage with unit and integration tests and enforce the thresholds
	mkdir -p coverage
	go test -count=1 -tags integration -coverpkg=./internal/...,./cmd/... -coverprofile=$(COVER_PROFILE) ./...
	go run ./scripts/covercheck $(COVER_PROFILE)

web-coverage: web-install ## Measure web coverage of the logic modules and enforce the thresholds
	cd web && npm run coverage

.PHONY: perf

perf: ## Measure start-up time, idle memory, bundle size and the performance tests (needs Docker)
	bash scripts/perf.sh
