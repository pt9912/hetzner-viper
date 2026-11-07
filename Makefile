# Everything runs in containers: only Docker (and make) are needed, not Go. With Go and golangci-lint
# installed (the devcontainer made by u-boot, see tools/mk/devcontainer.mk) the targets use them directly.
VERSION ?= $(shell git describe --tags --match 'v[0-9]*' --dirty 2>/dev/null || echo dev)
GOOS    ?= $(shell uname -s | tr A-Z a-z)
GOARCH  ?= $(shell uname -m | sed -e 's/x86_64/amd64/' -e 's/aarch64/arm64/')
EXT     := $(if $(filter windows,$(GOOS)),.exe)
DEV     := viper-dev
TOOLS_LOCAL := $(shell command -v go >/dev/null 2>&1 && command -v golangci-lint >/dev/null 2>&1 && echo 1)
LINT_ARGS := run --concurrency 2 --max-issues-per-linter 0 --max-same-issues 0 ./...
ifeq ($(TOOLS_LOCAL),1)
IMAGES   :=
LINT_IMG :=
RUN      :=
LINT_RUN := golangci-lint $(LINT_ARGS)
else
IMAGES   := image
LINT_IMG := lint-image
RUN      := docker run --rm -v "$(CURDIR)":/src -v viper-gocache:/home/dev/go -v viper-buildcache:/home/dev/.cache $(DEV)
LINT_RUN := docker run --rm -v "$(CURDIR)":/src -v viper-lintcache:/root/.cache -e GOMAXPROCS=2 viper-lint golangci-lint $(LINT_ARGS)
endif

.PHONY: help build dist image lint-image test vet fmt lint gates check

# The gates in the order of a serial run (not guaranteed under make -j). The fragments in tools/mk/
# put themselves in front (GATE_CHECKS := <target> $(GATE_CHECKS)), before gates is defined below.
GATE_CHECKS := fmt vet lint test
.DEFAULT_GOAL := help

include tools/mk/*.mk

help: ## this help
	@grep -hE '^[a-z0-9-]+:.*##' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*##"}{printf "  %-8s %s\n",$$1,$$2}'

build: ## viper binary for this computer into dist/
	DOCKER_BUILDKIT=1 docker build --target binary --build-arg VERSION=$(VERSION) \
	  --build-arg GOOS=$(GOOS) --build-arg GOARCH=$(GOARCH) --output type=local,dest=dist .
	@echo "built dist/viper$(EXT) ($(VERSION), $(GOOS)/$(GOARCH))"

dist: ## all release binaries and SHA256SUMS into dist/ (make dist VERSION=v0.10.0)
	rm -f dist/viper_* dist/SHA256SUMS
	DOCKER_BUILDKIT=1 docker build --progress=plain --target dist --build-arg VERSION=$(VERSION) --output type=local,dest=dist .
	@ls dist

image: ## development image (Go and the test tools)
	docker build --target dev --build-arg UID=$$(id -u) --build-arg GID=$$(id -g) -t $(DEV) .

test: $(IMAGES) ## all tests, as in the CI (tools missing = failure)
	$(RUN) go test -race ./...

vet: $(IMAGES) ## go vet
	$(RUN) go vet ./...

fmt: $(IMAGES) ## list files that gofmt would change
	$(RUN) sh -c 'o=$$(find . -name .cache -prune -o -name "*.go" -print | xargs gofmt -l); test -z "$$o" || { echo "$$o"; exit 1; }'

lint-image: ## golangci-lint image
	docker build --target lint -t viper-lint .

lint: $(LINT_IMG) ## golangci-lint (.golangci.yml)
	$(LINT_RUN)

gates: $(GATE_CHECKS) ## all gates in the order of GATE_CHECKS (what the CI runs)

check: gates ## the same as gates
