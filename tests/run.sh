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
  (cd "$ROOT" && cp -r cloud-init.yaml common.sh connect.sh dns.sh preflight.sh setup.sh start.sh stop.sh .env.local.example machines "$T/work/")
  rm -f "$T/work/machines/"*.env.local
  cp "$T/work/.env.local.example" "$T/work/.env.local"
  printf 'DNS_DOMAIN=test.invalid\nWORKSPACE_DIR=%s\n' "$T/mnt" >> "$T/work/.env.local"
  export HOME="$T/home" MOCK_STATE="$T/state" PATH="$MOCKS:$PATH"
  export HCLOUD_TOKEN=t CLOUDFLARE_API_TOKEN=t CLOUDFLARE_ZONE_ID=z
  unset MOCK_SSH_DOWN MOCK_DNS_STALE MOCK_CLOUDINIT_RC MOCK_HCLOUD_FAIL MOCK_CF_FAIL MOCK_INSTALL_FAIL MOCK_DOCKER_FAIL MOCK_DESCRIBE_FAIL MOCK_SSH_HOSTKEY_FAIL MOCK_SOLD_OUT MOCK_DEPRECATED MOCK_CF_ZONE MOCK_GH_BAD MOCK_GH_SCOPES MOCK_GH_NOREPO GH_TOKEN GH_HOST VIPER_GH_TOKEN VIPER_GH_HOST
  touch "$T/state/claude-ready"
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
    out=$( (set -e; "$fn" 3>&2) 2>&1 ); rc=$?
    if (( rc == 0 )); then PASS=$((PASS+1)); echo "ok   $fn"; grep '^ *skipped:' <<<"$out" || true
    else FAIL=$((FAIL+1)); echo "FAIL $fn"; echo "$out" | tail -25 | sed 's/^/    /'; fi
    for pid in "$T"/*/pid; do [[ -f "$pid" ]] && kill "$(cat "$pid")" 2>/dev/null; done
    rm -rf "$T"
  done < <(grep -oE '^test_[a-z0-9_]+' "$t")
done
echo; echo "$PASS passed, $FAIL failed"
(( FAIL == 0 ))
