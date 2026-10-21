#!/usr/bin/env bash
# Offline tests: the scripts run against fake hcloud/curl/ssh/scp (tests/mocks). No network, no cost.
# Usage: tests/run.sh [name-filter]
set -uo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
MOCKS="$ROOT/tests/mocks"
FILTER="${1:-}"
PASS=0; FAIL=0

new_env() {  # fresh sandbox: copy of the scripts, own HOME and mock state
  T=$(mktemp -d)
  mkdir -p "$T/work" "$T/home" "$T/state" "$T/mnt"
  (cd "$ROOT" && cp -r check.sh cloud-init.yaml common.sh connect.sh dns.sh preflight.sh setup.sh start.sh stop.sh .env.local.example machines "$T/work/")
  rm -f "$T/work/machines/"*.env.local
  cp "$T/work/.env.local.example" "$T/work/.env.local"
  printf 'DNS_DOMAIN=test.invalid\nWORKSPACE_DIR=%s\n' "$T/mnt" >> "$T/work/.env.local"
  export HOME="$T/home" MOCK_STATE="$T/state" PATH="$MOCKS:$PATH"
  export HCLOUD_TOKEN=t CLOUDFLARE_API_TOKEN=t CLOUDFLARE_ZONE_ID=z
  unset MOCK_SSH_DOWN MOCK_DNS_STALE MOCK_CLOUDINIT_RC MOCK_HCLOUD_FAIL MOCK_CF_FAIL MOCK_CF_WRITE_FAIL MOCK_INSTALL_FAIL MOCK_DOCKER_FAIL MOCK_DESCRIBE_FAIL MOCK_SSH_HOSTKEY_FAIL MOCK_CLAUDE_ANSWER MOCK_BREW_FAIL MOCK_BREW_FAIL_PKG MOCK_BREW_FAIL_TAP MOCK_BREW_FAIL_TRUST VIPER_HOMEBREW VIPER_BREW_TAPS VIPER_BREW_TRUST VIPER_BREW_PACKAGES MOCK_CLOUDINIT_DISABLED MOCK_SSH_AUTH_FAIL MOCK_CA_FAIL MOCK_SSHD_WEAK MOCK_SOLD_OUT MOCK_DEPRECATED MOCK_CF_ZONE MOCK_GH_BAD MOCK_GH_SCOPES MOCK_GH_NOREPO GH_TOKEN GH_HOST VIPER_GH_TOKEN VIPER_GH_HOST VIPER_CLAUDE_TOKEN CLAUDE_CODE_OAUTH_TOKEN VIPER_ANTHROPIC_BASE_URL VIPER_ANTHROPIC_AUTH_TOKEN VIPER_CLAUDE_MODEL VIPER_CLAUDE_MAX_CONTEXT_TOKENS MOCK_LLM_BAD_TOKEN MOCK_LLM_BAD_MODEL
  touch "$T/state/claude-ready"
  # a fake Homebrew where the installer would put it (common.sh reads VIPER_BREW)
  mkdir -p "$T/linuxbrew/bin"
  ln -s "$MOCKS/remote/brew" "$T/linuxbrew/bin/brew"
  export VIPER_BREW="$T/linuxbrew/bin/brew"
  : > "$MOCK_STATE/calls.log"
  W="$T/work"; CALLS="$MOCK_STATE/calls.log"
}
check() {  # check "description" command...
  local d="$1"; shift
  "$@" || { echo "ASSERT FAILED: $d"; return 1; }
}
# fails cmd...: the command must fail. (A bare `! cmd` is ignored by set -e and asserts nothing.)
fails() {
  if "$@"; then echo "ASSERT FAILED: expected failure: $*" >&3; return 1; fi
}
has() { grep -qE -- "$1" "$2"; }
hasf() { grep -qF -- "$1" "$2"; }
count_is() { [[ $(grep -cE -- "$1" "$2") == "$3" ]]; }
run() { "$W/$1" "${@:2}"; }

for t in "$ROOT"/tests/cases/*.sh; do
  source "$t"
  while IFS= read -r fn; do
    [[ -z "$FILTER" || "$fn" == *"$FILTER"* ]] || continue
    new_env
    # </dev/null: a test must not read the list of test names this loop reads from
    out=$( (set -e; "$fn" 3>&2) </dev/null 2>&1 ); rc=$?
    if (( rc == 0 )); then PASS=$((PASS+1)); echo "ok   $fn"; grep '^ *skipped:' <<<"$out" || true
    else FAIL=$((FAIL+1)); echo "FAIL $fn"; echo "$out" | tail -25 | sed 's/^/    /'; fi
    for pid in "$T"/*/pid; do [[ -f "$pid" ]] && kill "$(cat "$pid")" 2>/dev/null; done
    rm -rf "$T"
  done < <(grep -oE '^test_[a-z0-9_]+' "$t")
done
echo; echo "$PASS passed, $FAIL failed"
# every defined test must have run (guards against a test swallowing the loop's input)
if [[ -z "$FILTER" ]]; then
  defined=$(grep -hcE '^test_[a-z0-9_]+\(\)' "$ROOT"/tests/cases/*.sh | awk '{s += $1} END {print s}')
  (( PASS + FAIL == defined )) || { echo "ERROR: $defined tests defined, $((PASS + FAIL)) run"; exit 1; }
fi
(( FAIL == 0 ))
