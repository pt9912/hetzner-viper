#!/usr/bin/env bash
# tool-pruefung.sh: carry the tools the build needs the pinned versions?
#
# The pins are read from where they are set, not copied: go.mod (go), Dockerfile (GO_VERSION,
# GOLANGCI_LINT_VERSION). go and golangci-lint must report the pinned versions, and the tools of
# the build, the release and the tests must be installed (the devcontainer made by u-boot).
# The pins must also agree with each other (go.mod and Dockerfile name the same Go).
#
# Usage: tools/tool-pruefung.sh
# Exit 0: all fine, 1: a deviation (the message names the tool and the pin).
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"

fail() { echo "tool-pruefung: $1" >&2; exit 1; }

# the pins
go_mod=$(sed -n 's/^go[[:space:]][[:space:]]*\([0-9][0-9.]*\).*/\1/p' "$root/go.mod" | head -1)
go_pin=$(sed -n 's/^ARG GO_VERSION=\(.*\)$/\1/p' "$root/Dockerfile" | head -1)
lint_pin=$(sed -n 's/^ARG GOLANGCI_LINT_VERSION=v\(.*\)$/\1/p' "$root/Dockerfile" | head -1)
[ -n "$go_mod" ] || fail "the go directive of go.mod is unparseable"
[ -n "$go_pin" ] || fail "the GO_VERSION pin of the Dockerfile is unparseable"
[ -n "$lint_pin" ] || fail "the GOLANGCI_LINT_VERSION pin of the Dockerfile is unparseable"
[ "$go_mod" = "$go_pin" ] || fail "go.mod says go $go_mod, the Dockerfile pins GO_VERSION $go_pin"

# reports <tool> <command...>: the first line of the output, or a failure naming the tool
need() { command -v "$1" >/dev/null 2>&1 || fail "the tool $1 is not installed in the running context"; }

check_go() {   # $1 = output of "go version"
  case "$1" in
    "go version go$go_pin "*) : ;;
    *) fail "$2 reports \"$1\", the pin is go$go_pin" ;;
  esac
}
check_lint() {   # $1 = output of "golangci-lint --version"
  case "$1" in
    "golangci-lint has version $lint_pin "*) : ;;
    *) fail "$2 reports \"$1\", the pin is v$lint_pin" ;;
  esac
}

need go; need golangci-lint
check_go "$(go version 2>&1 | head -1)" "go"
check_lint "$(golangci-lint --version 2>&1 | head -1)" "golangci-lint"
# the build (make build, dist), the release (gh, curl) and the tests (VIPER_REQUIRE_TOOLS=1 makes a missing one a failure there, too)
for t in bash make git docker gh curl jq shellcheck ssh-keygen tmux; do need "$t"; done
# sshd runs the tests of the real server; without it they are skipped (VIPER_REQUIRE_TOOLS unset), so it only warns
command -v sshd >/dev/null 2>&1 || [ -x /usr/sbin/sshd ] || echo "tool-pruefung: WARNING: sshd is not installed, the tests with a real sshd are skipped" >&2
echo "tool-pruefung: the running context carries the pinned toolchain (go $go_pin, golangci-lint $lint_pin) and the tools of build, release and tests - OK"
