#!/usr/bin/env bash
# ci-devcontainer.sh: run make targets in the image of the devcontainer (.devcontainer/Dockerfile),
# the same toolchain as in the development. For the CI, where the devcontainer itself is not started
# (its devcontainer.json wants a volume as workspace and a devcontainer.env of the host).
#
# Usage: tools/ci-devcontainer.sh <make arguments...>   e.g. tools/ci-devcontainer.sh check
# The checkout is mounted at its own path, so the Docker daemon of the host (its socket is passed on
# for make pinsync) sees the same paths as the container.
set -euo pipefail
cd "$(dirname "$0")/.."
image=viper-devcontainer
docker build -t "$image" .devcontainer
# the user of the image (vscode, UID 1000) is not the owner of the checkout, but must write to it. Only what
# this user owns can be changed: a second call (make check, then make dist) finds files that the first call made
# as vscode (the built MCP server, .git/index), which belong to vscode and are writable for it anyway.
find . -user "$(id -u)" -exec chmod a+rwX {} +
sock=/var/run/docker.sock
docker run --rm --user vscode \
  -v "$PWD:$PWD" -w "$PWD" \
  -v "$sock:$sock" --group-add "$(stat -c %g "$sock")" \
  -e GOCACHE=/tmp/go-build -e GOMODCACHE=/tmp/go-mod \
  -e GOFLAGS="-mod=readonly -buildvcs=false" -e GOTOOLCHAIN=local \
  -e GIT_CONFIG_COUNT=1 -e GIT_CONFIG_KEY_0=safe.directory -e GIT_CONFIG_VALUE_0='*' \
  "$image" make "$@"
