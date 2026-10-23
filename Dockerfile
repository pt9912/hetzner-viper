# Targets: "test" (offline test suite) and "run" (real start/stop against Hetzner/Cloudflare).
# BASE_IMAGE: another source for the base image (make build picks Google's mirror itself when Docker Hub is unreachable)
ARG BASE_IMAGE=debian:bookworm-slim
FROM ${BASE_IMAGE} AS base
ARG TARGETARCH
ARG HCLOUD_VERSION=v1.70.1
ARG HCLOUD_SHA256_AMD64=85182e0d51f9a26abeefd79b37de8a7fb51215d54c59ee290c3c9141e8aa887e
ARG HCLOUD_SHA256_ARM64=c9dbc00158d31131527e9ac5cb59b835fe8275ab7922f627d81b4e9bc2a2cebb
RUN apt-get update \
 && apt-get install -y --no-install-recommends bash git jq curl openssh-client python3 python3-yaml make ca-certificates tzdata \
 && rm -rf /var/lib/apt/lists/*
RUN set -eu; \
    case "$TARGETARCH" in \
      amd64) sha="$HCLOUD_SHA256_AMD64" ;; \
      arm64) sha="$HCLOUD_SHA256_ARM64" ;; \
      *) echo "unsupported arch: $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    curl -fsSL -o /tmp/hcloud.tgz "https://github.com/hetznercloud/cli/releases/download/${HCLOUD_VERSION}/hcloud-linux-${TARGETARCH}.tar.gz"; \
    echo "$sha  /tmp/hcloud.tgz" | sha256sum -c -; \
    tar -xzf /tmp/hcloud.tgz -C /usr/local/bin hcloud; \
    rm /tmp/hcloud.tgz
WORKDIR /repo

FROM base AS test
# openssh-server: tests/cases/sshd.sh checks the ssh trust logic against a real (unprivileged) sshd
RUN apt-get update && apt-get install -y --no-install-recommends shellcheck openssh-server && rm -rf /var/lib/apt/lists/* \
 && mkdir -p /run/sshd
RUN useradd --create-home tester
USER tester
CMD ["make", "test"]

# The image is independent of the user: `make` runs it with the host's uid and gid (--user) and
# mounts a generated /etc/passwd with that uid and the host's home path, so that ~/.ssh and the
# ssh config written by the scripts are valid on the host too (ssh resolves ~ via the passwd
# entry, not $HOME). The same image therefore serves everyone, built locally or pulled.
FROM base AS run
LABEL org.opencontainers.image.source="https://github.com/pt9912/hetzner-viper" \
      org.opencontainers.image.description="Hetzner Viper: tools to run the scripts (ssh, hcloud, jq, ...)" \
      org.opencontainers.image.licenses="MIT"
ENTRYPOINT ["/bin/bash"]
