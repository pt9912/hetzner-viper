# syntax=docker/dockerfile:1
# viper-machine: the machine of the Kubernetes provider (docs/plan/kubernetes.md). Build from the root of the
# repository: docker build -f image/Dockerfile -t viper-machine .  (make image)
#
# Everything that needs root is done here, at build time. At run time the container is builder (uid 1000)
# without capabilities and without sudo, so it also starts under Pod Security "restricted".
ARG UBUNTU=ubuntu:26.04@sha256:065aa8626841f16057b78b027b464cd59c4fac36c204c72dcbf48d66f5fa8a96
FROM ${UBUNTU}
ENV DEBIAN_FRONTEND=noninteractive LC_ALL=C.UTF-8

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

# builder, the work directory and the place of Homebrew (the installer needs sudo only to make it)
RUN (id ubuntu >/dev/null 2>&1 && userdel -r ubuntu || true) \
 && useradd -m -u 1000 -s /bin/bash builder \
 && mkdir -p /Development /home/linuxbrew/.linuxbrew /opt/viper /run/viper \
 && chown builder:builder /Development /home/linuxbrew/.linuxbrew \
 && echo 'eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)"' > /etc/profile.d/brew.sh

# what the VM gets from cloud-init.yaml: the sshd hardening, tmux and the VS Code settings: the same text
COPY cloud-init.yaml /tmp/cloud-init.yaml
COPY image/seed.py /tmp/seed.py
RUN python3 /tmp/seed.py /tmp/cloud-init.yaml / \
 && chown -R builder:builder /home/builder \
 && rm /tmp/seed.py /tmp/cloud-init.yaml

COPY image/sshd_config /opt/viper/sshd_config
COPY --chmod=0755 image/entry.sh /opt/viper/entry.sh

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

USER 1000
ENV PATH=/home/linuxbrew/.linuxbrew/bin:/home/builder/.local/bin:$PATH
EXPOSE 2222
ENTRYPOINT ["/usr/bin/tini", "-g", "--", "/opt/viper/entry.sh"]
