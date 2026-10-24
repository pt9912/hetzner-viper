# Builds viper without a Go installation on the host: docker build --output exports dist/.
FROM golang:1.27 AS build
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
FROM golang:1.27 AS dev
RUN apt-get update && apt-get install -y --no-install-recommends jq shellcheck openssh-server openssh-client git tmux python3 \
 && mkdir -p /run/sshd && rm -rf /var/lib/apt/lists/*
WORKDIR /src
ENV VIPER_REQUIRE_TOOLS=1

# Lint: golangci-lint with .golangci.yml.
FROM golangci/golangci-lint:v2.15.0 AS lint
WORKDIR /src
