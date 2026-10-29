# Builds viper without a Go installation on the host: docker build --output exports the files.
ARG GO_VERSION=1.27.3
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

# Development image: Go plus the tools the tests need (same as the CI installs).
FROM golang:${GO_VERSION} AS dev
# The tests run as the user of the host (UID/GID): some of them need a user who is not root
# (an unwritable ~/.ssh/config), and ssh-keygen needs a passwd entry for the user.
ARG UID=1000
ARG GID=1000
RUN apt-get update && apt-get install -y --no-install-recommends jq shellcheck openssh-server openssh-client git tmux python3 \
 && mkdir -p /run/sshd && rm -rf /var/lib/apt/lists/*
RUN (getent group $GID >/dev/null || groupadd -g $GID dev) \
 && useradd -m -u $UID -g $GID -d /home/dev -s /bin/bash dev \
 && mkdir -p /home/dev/go /home/dev/.cache && chown -R $UID:$GID /home/dev
USER dev
WORKDIR /src
ENV HOME=/home/dev GOPATH=/home/dev/go GOCACHE=/home/dev/.cache/go-build VIPER_REQUIRE_TOOLS=1

# Lint: golangci-lint with .golangci.yml.
FROM golangci/golangci-lint:v2.15.0 AS lint
WORKDIR /src
