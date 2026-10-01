.DEFAULT_GOAL := help
# OAuth terminal tests share fixed loopback ports across verification targets.
.NOTPARALLEL:

GO ?= go
GOLANGCI_VERSION := 2.14.0
GOVULNCHECK_VERSION := v1.8.0
HOST_OS := $(shell $(GO) env GOHOSTOS)
HOST_ARCH := $(shell $(GO) env GOHOSTARCH)
GOLANGCI := .tools/golangci-lint-$(GOLANGCI_VERSION)-$(HOST_OS)-$(HOST_ARCH)/golangci-lint
GOVULNCHECK := $(GO) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

.PHONY: help tools fmt lint vet test race vuln tidy-check build check release

help:
	@printf '%s\n' \
	  'make tools       Download pinned development tools into the local cache' \
	  'make fmt         Format Go files and organize imports' \
	  'make lint        Check formatting and run static analysis' \
	  'make vet         Run Go vet' \
	  'make test        Run all tests, including terminal scenarios' \
	  'make race        Run tests with the race detector' \
	  'make vuln        Check reachable dependency vulnerabilities' \
	  'make tidy-check  Verify module files are tidy without changing them' \
	  'make build       Build ./yz' \
	  'make check       Run lint, module checks, tests, build, and vulnerability scan' \
	  'make release     Build all release binaries with the existing release script'

$(GOLANGCI): scripts/install-golangci-lint.sh
	bash scripts/install-golangci-lint.sh $(GOLANGCI_VERSION) $(HOST_OS) $(HOST_ARCH)

tools: $(GOLANGCI)
	$(GOLANGCI) version
	$(GOVULNCHECK) -version

fmt: $(GOLANGCI)
	$(GOLANGCI) fmt ./...

lint: $(GOLANGCI)
	$(GOLANGCI) run ./...

vet:
	$(GO) vet ./...

test:
	$(GO) test ./... -count=1

race:
	GORACE="$${GORACE:-atexit_sleep_ms=0}" YZ_E2E_RACE=1 $(GO) test -race ./... -count=1

vuln:
	$(GOVULNCHECK) ./...

tidy-check:
	$(GO) mod tidy -diff

build:
	$(GO) build -trimpath -o yz ./cmd/yz

check:
	$(MAKE) lint
	$(MAKE) tidy-check
	$(MAKE) test
	$(MAKE) build
	$(MAKE) vuln

release:
	bash scripts/release.sh
