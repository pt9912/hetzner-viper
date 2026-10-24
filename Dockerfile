# Builds viper without a Go installation on the host: docker build --output exports dist/.
ARG GO_VERSION=1.27.3
FROM golang:${GO_VERSION} AS build
ARG VERSION=dev
ARG GOOS=linux
ARG GOARCH=amd64
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=${GOOS} GOARCH=${GOARCH} go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/viper ./cmd/viper

FROM scratch AS binary
COPY --from=build /out/viper /viper

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
