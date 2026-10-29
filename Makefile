# Everything runs in containers: only Docker (and make) are needed, not Go.
VERSION ?= $(shell git describe --tags --match 'v[0-9]*' --dirty 2>/dev/null || echo dev)
GOOS    ?= $(shell uname -s | tr A-Z a-z)
GOARCH  ?= $(shell uname -m | sed -e 's/x86_64/amd64/' -e 's/aarch64/arm64/')
EXT     := $(if $(filter windows,$(GOOS)),.exe)
DEV     := viper-dev
RUN     := docker run --rm -v "$(CURDIR)":/src -v viper-gocache:/home/dev/go -v viper-buildcache:/home/dev/.cache $(DEV)

.PHONY: help build dist image test vet fmt lint check
.DEFAULT_GOAL := help

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

test: image ## all tests, as in the CI (tools missing = failure)
	$(RUN) go test -race ./...

vet: image ## go vet
	$(RUN) go vet ./...

fmt: image ## list files that gofmt would change
	$(RUN) sh -c 'test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }'

lint: ## golangci-lint (.golangci.yml)
	docker build --target lint -t viper-lint .
	docker run --rm -v "$(CURDIR)":/src -v viper-lintcache:/root/.cache -e GOMAXPROCS=2 viper-lint golangci-lint run --concurrency 2 --max-issues-per-linter 0 --max-same-issues 0 ./...

check: fmt vet lint test ## what the CI runs
