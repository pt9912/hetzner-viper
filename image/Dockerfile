# syntax=docker/dockerfile:1
# viper-machine: the machine of the Kubernetes provider (docs/plan/kubernetes.md). Build from the root of the
# repository: docker build -f image/Dockerfile -t viper-machine .  (make image)
#
# Everything that needs root is done here, at build time. At run time the container is builder (uid 1000)
# without capabilities and without sudo, so it also starts under Pod Security "restricted".
ARG UBUNTU=ubuntu:26.04@sha256:065aa8626841f16057b78b027b464cd59c4fac36c204c72dcbf48d66f5fa8a96
FROM ${UBUNTU}
ENV DEBIAN_FRONTEND=noninteractive LC_ALL=C.UTF-8

# The NATS programs that viper puts on the machines of a NATS group (internal/natsserver: Version, CLIVersion and the
# checksums). They are the same releases with the same sums as on a VM; internal/natsserver/image_test.go fails when
# these lines and the Go code differ. Set by docker for the platform being built (amd64, arm64).
ARG TARGETARCH
ARG NATS_SERVER_VERSION=v2.15.2
ARG NATS_SERVER_SHA256_AMD64=67cb8e10bc6f0c4b47f75a728529a20953dbae2a4f9a4a52ae8517d65ae303ff
ARG NATS_SERVER_SHA256_ARM64=bc2c788dcbf2849af749564e06a0d48c19bced851f2d68a2e21c54d078949b28
ARG NATS_CLI_VERSION=v0.5.1
ARG NATS_CLI_SHA256_AMD64=f8a3e106deebed7afe736b7090359bda3aa62a04b310440bedc1d11a93c0ab4f
ARG NATS_CLI_SHA256_ARM64=91c661f1173ee9bfe07514eef55a29d25ac7757a0868c179e825eba976073adc

# the packages of cloud-init.yaml (without the ones only a VM needs), tini as PID 1, python3-yaml for image/seed.py
RUN apt-get update && apt-get install -y --no-install-recommends \
      gnupg git gh make build-essential curl ca-certificates tmux jq openssh-server psmisc procps file \
      tini python3 python3-yaml \
 && rm -rf /var/lib/apt/lists/*

# the Docker client and its plugins from Docker's repository (the daemon is the sidecar or a remote one, K8S_DOCKER);
# the signing key must have Docker's published fingerprint, as in cloud-init.yaml
RUN set -eu; \
    install -m 0755 -d /etc/apt/keyrings; \
    curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc; \
    gpg --show-keys --with-colons /etc/apt/keyrings/docker.asc | grep -q '^fpr:::::::::9DC858229FC7DD38854AE2D88D81803C0EBFCD88:'; \
    . /etc/os-release; \
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu $VERSION_CODENAME stable" > /etc/apt/sources.list.d/docker.list; \
    apt-get update; \
    apt-get install -y --no-install-recommends docker-ce-cli docker-buildx-plugin docker-compose-plugin; \
    rm -rf /var/lib/apt/lists/*

# nats-server and the nats tool in /usr/local/bin, where the scripts of viper look (they skip the download when the
# version is the one they want, so they need no sudo here). Checked against the sums above before anything is unpacked.
RUN set -eu; \
    case "$TARGETARCH" in \
      amd64) srv_sum="$NATS_SERVER_SHA256_AMD64"; cli_sum="$NATS_CLI_SHA256_AMD64" ;; \
      arm64) srv_sum="$NATS_SERVER_SHA256_ARM64"; cli_sum="$NATS_CLI_SHA256_ARM64" ;; \
      *) echo "no NATS release pinned for $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    tmp=$(mktemp -d); \
    curl -fsSL --retry 3 -o "$tmp/srv.tgz" "https://github.com/nats-io/nats-server/releases/download/${NATS_SERVER_VERSION}/nats-server-${NATS_SERVER_VERSION}-linux-${TARGETARCH}.tar.gz"; \
    echo "$srv_sum  $tmp/srv.tgz" | sha256sum -c --quiet -; \
    tar -xzf "$tmp/srv.tgz" -C "$tmp" --strip-components=1 --wildcards '*/nats-server'; \
    install -m 0755 "$tmp/nats-server" /usr/local/bin/nats-server; \
    curl -fsSL --retry 3 -o "$tmp/cli.zip" "https://github.com/nats-io/natscli/releases/download/${NATS_CLI_VERSION}/nats-${NATS_CLI_VERSION#v}-linux-${TARGETARCH}.zip"; \
    echo "$cli_sum  $tmp/cli.zip" | sha256sum -c --quiet -; \
    python3 -m zipfile -e "$tmp/cli.zip" "$tmp/cli"; \
    install -m 0755 "$(find "$tmp/cli" -type f -name nats | head -n 1)" /usr/local/bin/nats; \
    rm -rf "$tmp"; \
    nats-server --version; nats --version

# builder and the place of Homebrew (the installer needs sudo only to make it)
RUN (id ubuntu >/dev/null 2>&1 && userdel -r ubuntu || true) \
 && useradd -m -u 1000 -s /bin/bash builder \
 && mkdir -p /home/linuxbrew/.linuxbrew /opt/viper /run/viper \
 && chown builder:builder /home/linuxbrew/.linuxbrew \
 && echo 'eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)"' > /etc/profile.d/brew.sh

# what the VM gets from cloud-init.yaml: the sshd hardening, tmux and the VS Code settings: the same text
COPY cloud-init.yaml /tmp/cloud-init.yaml
COPY image/seed.py /tmp/seed.py
RUN python3 /tmp/seed.py /tmp/cloud-init.yaml / \
 && chown -R builder:builder /home/builder \
 && rm /tmp/seed.py /tmp/cloud-init.yaml

USER builder
ENV HOME=/home/builder
WORKDIR /home/builder

# Homebrew, installed by the official installer as builder (it is in the image, not in the home directory)
RUN set -eu; \
    curl -fsSL -o /tmp/brew-install.sh https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh; \
    NONINTERACTIVE=1 bash /tmp/brew-install.sh </dev/null >/tmp/brew-install.log 2>&1 || { tail -n 20 /tmp/brew-install.log; exit 1; }; \
    rm -f /tmp/brew-install.sh /tmp/brew-install.log

# Claude Code (the installer is rate limited per address: retry with growing pauses, like cloud-init.yaml)
RUN set -eu; for i in 1 2 3 4 5; do \
      if curl -fsSL https://claude.ai/install.sh | bash; then exit 0; fi; \
      [ "$i" -lt 5 ] && sleep $((i * 30)); \
    done; exit 1

# a volume over /home/builder hides all this: keep a copy that the entrypoint brings to an empty volume
USER root
RUN mkdir -p /opt/viper/home-seed && cp -a /home/builder/. /opt/viper/home-seed/ && chown -R builder:builder /opt/viper/home-seed

# last, so that a change of the entrypoint does not make the long steps above run again
COPY image/sshd_config /opt/viper/sshd_config
COPY --chmod=0755 image/entry.sh /opt/viper/entry.sh

USER 1000
ENV PATH=/home/linuxbrew/.linuxbrew/bin:/home/builder/.local/bin:$PATH
EXPOSE 2222
ENTRYPOINT ["/usr/bin/tini", "-g", "--", "/opt/viper/entry.sh"]
