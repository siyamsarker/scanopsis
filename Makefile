# Copyright 2026 The Scanopsis Authors
# SPDX-License-Identifier: Apache-2.0

# Pinned tool versions. Tools run through `go run`, so nothing is installed
# globally and every contributor and CI job uses the same version.
GOLANGCI_LINT := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0
GOVULNCHECK   := go run golang.org/x/vuln/cmd/govulncheck@v1.10.0

# Restrict VCS-derived values to a safe character set: tag names may contain
# shell metacharacters, and these values are spliced into command lines.
SAFE    := tr -cd 'A-Za-z0-9._+-'
VERSION ?= $(shell (git describe --tags --always --dirty 2>/dev/null || echo dev) | $(SAFE))
COMMIT  ?= $(shell (git rev-parse --short HEAD 2>/dev/null || echo unknown) | $(SAFE))
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)
IMAGE   ?= scanopsis:dev

.PHONY: help check fmt fmt-check lint test vuln build web-install web-lint web-test web-build web image clean

help: ## List available targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-12s %s\n", $$1, $$2}'

check: fmt-check lint test vuln web-lint web-test build ## Run every check that CI runs, except the image build

fmt: ## Format Go code
	$(GOLANGCI_LINT) fmt

fmt-check: ## Fail if Go code is not formatted
	$(GOLANGCI_LINT) fmt --diff

lint: ## Lint Go code
	$(GOLANGCI_LINT) run

test: ## Run Go tests with the race detector
	go test -race -count=1 ./...

vuln: ## Check Go dependencies for known vulnerabilities
	$(GOVULNCHECK) ./...

build: web-build ## Build bin/scanopsis with the web interface embedded
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/scanopsis ./cmd/scanopsis

web-install: ## Install web dependencies exactly as locked
	cd web && npm ci

web-lint: web-install ## Lint and type-check the web interface
	cd web && npm run lint

web-test: web-install ## Run web unit tests
	cd web && npm test

web-build: web-install ## Build the web interface into web/dist/app
	cd web && npm run build

web: web-build ## Alias for web-build

image: ## Build the container image
	docker build --build-arg VERSION='$(VERSION)' --build-arg COMMIT='$(COMMIT)' -t '$(IMAGE)' .

clean: ## Remove build output
	rm -rf bin web/dist/app
