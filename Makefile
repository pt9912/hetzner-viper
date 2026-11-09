# With Go and golangci-lint installed (the devcontainer made by u-boot, see tools/mk/devcontainer.mk) every target uses them directly.
VERSION ?= $(shell git describe --tags --match 'v[0-9]*' --dirty 2>/dev/null || echo dev)
GOOS    ?= $(shell uname -s | tr A-Z a-z)
GOARCH  ?= $(shell uname -m | sed -e 's/x86_64/amd64/' -e 's/aarch64/arm64/')
EXT     := $(if $(filter windows,$(GOOS)),.exe)
LINT_ARGS := run --concurrency 2 --max-issues-per-linter 0 --max-same-issues 0 ./...
PLATFORMS ?= linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64
LDFLAGS    = -s -w -X main.version=$(VERSION)
# viper-nats-mcp is built into viper (internal/mcpbin/bin) for the Linux servers: same bytes in every build
MCPDIR     = internal/mcpbin/bin

.PHONY: help build mcp dist dist-docker test vet fmt lint gates check

# The gates in the order of a serial run (not guaranteed under make -j). The fragments in tools/mk/
# put themselves in front (GATE_CHECKS := <target> $(GATE_CHECKS)), before gates is defined below.
GATE_CHECKS := fmt vet lint test
.DEFAULT_GOAL := help

include tools/mk/*.mk

help: ## this help
	@grep -hE '^[a-z0-9-]+:.*##' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*##"}{printf "  %-8s %s\n",$$1,$$2}'

mcp: ## viper-nats-mcp for the Linux servers (amd64, arm64) into internal/mcpbin/bin, to be built into viper
	@mkdir -p $(MCPDIR)
	@set -eu; for arch in amd64 arm64; do \
	  CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=$(VERSION)" \
	    -o "$(MCPDIR)/viper-nats-mcp_linux_$$arch" ./cmd/viper-nats-mcp; \
	done
	@echo "built $(MCPDIR)/viper-nats-mcp_linux_{amd64,arm64} ($(VERSION))"

build: mcp ## viper binary for this computer into dist/ (with the NATS MCP server inside)
	@mkdir -p dist
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -trimpath -ldflags "$(LDFLAGS)" -o "dist/viper$(EXT)" ./cmd/viper
	@echo "built dist/viper$(EXT) ($(VERSION), $(GOOS)/$(GOARCH))"

dist: mcp ## all release binaries and SHA256SUMS into dist/ (make dist VERSION=v0.10.0)
	rm -f dist/viper_* dist/SHA256SUMS
	@mkdir -p dist
	@set -eu; for t in $(PLATFORMS); do \
	  os=$${t%/*}; arch=$${t#*/}; ext=""; [ "$$os" = windows ] && ext=".exe"; \
	  echo "building $$t"; \
	  CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" \
	    -o "dist/viper_$(VERSION)_$${os}_$${arch}$$ext" ./cmd/viper; \
	done; \
	cd dist && sha256sum viper_* > SHA256SUMS
	@ls dist

dist-docker: ## like dist, but built by the Dockerfile (needs only Docker, no Go)
	rm -f dist/viper_* dist/SHA256SUMS
	DOCKER_BUILDKIT=1 docker build --progress=plain --target dist --build-arg VERSION=$(VERSION) --output type=local,dest=dist .
	@ls dist

test: mcp ## all tests (VIPER_REQUIRE_TOOLS=1: a missing tool fails; VIPER_REQUIRE_MCP=1: the built-in MCP server must be there)
	VIPER_REQUIRE_TOOLS=1 VIPER_REQUIRE_MCP=1 go test -race ./...

vet: ## go vet
	go vet ./...

fmt: ## list files that gofmt would change
	sh -c 'o=$$(find . -name .cache -prune -o -name "*.go" -print | xargs gofmt -l); test -z "$$o" || { echo "$$o"; exit 1; }'

lint: ## golangci-lint (.golangci.yml)
	golangci-lint $(LINT_ARGS)

gates: $(GATE_CHECKS) ## all gates in the order of GATE_CHECKS (what the CI runs)

check: gates ## the same as gates
