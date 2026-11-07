# Builds viper without a Go installation on the host (make dist-docker): docker build --output exports the files.
# The development happens in the devcontainer (.devcontainer/, made by u-boot); the ARGs below are pins.
ARG GO_VERSION=1.27.3
ARG GOLANGCI_LINT_VERSION=v2.15.0
FROM golang:${GO_VERSION} AS src
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

# One binary for GOOS/GOARCH (viper, or viper.exe for windows): make build.
FROM src AS build
ARG VERSION=dev
ARG GOOS=linux
ARG GOARCH=amd64
RUN ext=""; if [ "$GOOS" = windows ]; then ext=".exe"; fi; \
    CGO_ENABLED=0 GOOS=${GOOS} GOARCH=${GOARCH} go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o "/out/viper${ext}" ./cmd/viper

FROM scratch AS binary
COPY --from=build /out/ /

# All platforms of a release with SHA256SUMS (viper_<version>_<os>_<arch>[.exe]): make dist.
FROM src AS dist-build
ARG VERSION=dev
ARG PLATFORMS="linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64"
RUN --mount=type=cache,target=/root/.cache/go-build \
    set -eu; mkdir /out; \
    for target in ${PLATFORMS}; do \
      os=${target%/*}; arch=${target#*/}; ext=""; \
      if [ "$os" = windows ]; then ext=".exe"; fi; \
      CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
        -o "/out/viper_${VERSION}_${os}_${arch}${ext}" ./cmd/viper; \
    done; \
    cd /out && sha256sum viper_* > SHA256SUMS

FROM scratch AS dist
COPY --from=dist-build /out/ /
