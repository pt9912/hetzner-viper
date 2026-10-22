#!/usr/bin/env bash
# Read-only checks of a running machine: DNS, access like another device (host CA + DNS
# name, as an iPad or a second computer sees it) and the state of the server.
# Usage: ./check.sh <machine>
set -euo pipefail
MACHINE="${1:?Usage: ./check.sh <machine>}"; export MACHINE
source "$(dirname "$0")/common.sh"
start_transcript check
resolve_repo_url ""

failures=0
ok()   { echo "ok    $*"; }
bad()  { echo "FAIL  $*"; failures=$((failures + 1)); }
info() { echo "info  $*"; }
warn() { echo "WARN  $*"; }

hcloud_has server "$SERVER_NAME" || { echo "Server $SERVER_NAME does not exist (start it with ./start.sh $MACHINE)" >&2; exit 1; }
IP=$(hcloud server ip "$SERVER_NAME")
[[ -f "$SSH_CFG" ]] || write_ssh_config "$IP"

echo "== DNS ($DNS_HOST)"
# public resolvers via DNS over HTTPS (no dig needed)
for doh in "Cloudflare 1.1.1.1|https://cloudflare-dns.com/dns-query" "Google 8.8.8.8|https://dns.google/resolve"; do
  name="${doh%%|*}"; url="${doh#*|}"
  if answer=$(curl -fsS -H 'accept: application/dns-json' "$url?name=$DNS_HOST&type=A" 2>/dev/null); then
    addrs=$(jq -r '[.Answer[]? | select(.type == 1) | .data] | join(",")' <<<"$answer")
    if [[ ",$addrs," == *",$IP,"* ]]; then
      ok "$name resolves to $IP"
    else
      bad "$name resolves to '${addrs:-nothing}', expected $IP (new record? wait a few minutes)"
    fi
  else
    warn "$name could not be asked"
  fi
done
# the resolver this process uses (in the make container: the container's, not the host's)
where="this computer's resolver"; [[ -f /.dockerenv ]] && where="the container's resolver"
where="$where ($(awk '/^nameserver/ {print $2; exit}' /etc/resolv.conf 2>/dev/null))"
if local_addr=$(getent ahostsv4 "$DNS_HOST" 2>/dev/null | awk 'NR == 1 {print $1}') && [[ "$local_addr" == "$IP" ]]; then
  ok "$where resolves it to $IP"
else
  # A resolver that looked the name up while it did not exist (e.g. between down and up)
  # may keep answering "no such name" for the zone's negative-caching time (last SOA field).
  soa=$(curl -fsS -H 'accept: application/dns-json' "https://cloudflare-dns.com/dns-query?name=$DNS_DOMAIN&type=SOA" 2>/dev/null |
        jq -r '[.Answer[]?, .Authority[]? | select(.type == 6) | .data][0] // empty' 2>/dev/null || true)
  neg_ttl=$(awk '{print $NF}' <<<"$soa")
  info "$where resolves it to '${local_addr:-nothing}'. If the public resolvers above are right, it still"
  info "  remembers 'no such name' from a lookup while the record did not exist, for up to ${neg_ttl:-?} s (often a VPN's DNS server)"
fi

echo "== SSH like another device (host CA, DNS name)"
ca_known="$HOME/.ssh/known_hosts_hetzner_claude_ca"
if err=$(ssh -n -F /dev/null -i "$SSH_KEY" -o IdentitiesOnly=yes -o BatchMode=yes -o ConnectTimeout=10 \
      -o StrictHostKeyChecking=yes -o UserKnownHostsFile="$ca_known" \
      -o HostKeyAlgorithms=ssh-ed25519-cert-v01@openssh.com "builder@$DNS_HOST" true 2>&1); then
  ok "connection to $DNS_HOST verified by the host CA (certificate valid for this name)"
else
  bad "connection to $DNS_HOST with the host CA failed: $(tail -n1 <<<"$err")"
fi
# expiry of the host certificate (only informative; ssh-keyscan -c prints "<type> <base64>")
cert=$(ssh-keyscan -c -t ed25519 "$DNS_HOST" 2>/dev/null | grep -m1 -oE 'ssh-ed25519-cert-v01@openssh\.com [A-Za-z0-9+/=]+' || true)
valid=$( [[ -z "$cert" ]] || ssh-keygen -L -f <(echo "$cert") 2>/dev/null | sed -n 's/^ *Valid: //p' || true)
if [[ -n "$valid" ]]; then
  info "host certificate $valid; afterwards only computers with a pinned host key can verify the server"
else
  info "host certificate validity could not be read"
fi

echo "== server ($SERVER_NAME, $IP)"
WORKDIR="$WORKSPACE_DIR"
[[ -z "${REPO_URL:-}" ]] || WORKDIR="$WORKSPACE_DIR/$(basename "${REPO_URL%.git}")"
# key=value lines; nothing is changed on the server
if ! out=$(ssh -F "$SSH_CFG" "$SSH_ALIAS" "bash -s -- $(printf '%q ' "$WORKDIR" "${VIPER_CLAUDE_TOKEN:+1}${VIPER_ANTHROPIC_BASE_URL:+1}" "$BREW" "${VIPER_BREW_TAPS:-}" "${VIPER_BREW_PACKAGES:-}" "$( [[ "${VIPER_BREWFILE:-0}" != 1 || -z "${REPO_URL:-}" ]] || echo "$WORKDIR/Brewfile")" "${VIPER_BREW_TRUST:-}" "$VIPER_CODE_SERVER" "$CODE_SERVER_PORT")" <<'PROBE'
set -uo pipefail
dir="$1"; ask_claude="$2"; brew="$3"; taps_in="$4"; pkgs_in="$5"; brewfile="$6"; trust_in="$7"; cs="$8"; cs_port="$9"
export LC_ALL=C PATH="$HOME/.local/bin:$PATH"
echo "cloudinit=$(cloud-init status 2>/dev/null | sed -n 's/^status: //p')"
# errors of the first boot stay recorded here, also once cloud-init is disabled
# (VIPER_CLOUD_RESULT only exists for the offline tests)
result="${VIPER_CLOUD_RESULT:-/var/lib/cloud/data/result.json}"
echo "cloudinit_errors=$(jq -r '.v1.errors | length' "$result" 2>/dev/null)"
echo "cloudinit_error=$(jq -r '.v1.errors[0] // empty' "$result" 2>/dev/null | head -c 200)"
echo "claude_version=$(claude --version 2>/dev/null | head -n1)"
# the same loader ssh commands, tmux and VS Code terminals use
echo "claude_auth=$(bash -c '. ~/.bashrc >/dev/null 2>&1
  if [ -n "${ANTHROPIC_BASE_URL:-}" ]; then echo "gateway ${ANTHROPIC_MODEL:-} ${ANTHROPIC_BASE_URL}"
  elif [ -n "${CLAUDE_CODE_OAUTH_TOKEN:-}" ]; then echo token; else echo none; fi')"
if [ -n "$ask_claude" ]; then
  # </dev/null: claude -p reads stdin as extra prompt and would swallow the rest of this script
  answer=$(cd "$dir" 2>/dev/null || cd; bash -c '. ~/.bashrc >/dev/null 2>&1; timeout 120 claude -p "Reply with exactly: OK"' </dev/null 2>&1 | tail -n1)
  echo "claude_answer=$answer"
fi
echo "gh_version=$(gh --version 2>/dev/null | head -n1)"
if gh auth status >/dev/null 2>&1; then echo gh_auth=yes; else echo gh_auth=no; fi
echo "git_name=$(git config --global user.name 2>/dev/null)"
echo "git_email=$(git config --global user.email 2>/dev/null)"
if docker info >/dev/null 2>&1; then echo docker=ok; else echo docker=no; fi
echo "swap_mb=$(swapon --show=SIZE --bytes --noheadings 2>/dev/null | awk '{s += $1} END {printf "%d", s / 1048576}')"
if [ "$cs" = 1 ]; then
  echo "code_server_version=$(code-server --version 2>/dev/null | head -n1)"
  echo "code_server_active=$(systemctl is-active code-server@builder 2>/dev/null)"
  if curl -fsS -m 5 -o /dev/null "http://127.0.0.1:$cs_port/healthz" 2>/dev/null; then echo code_server_http=yes; else echo code_server_http=no; fi
fi
echo "brew_version=$("$brew" --version 2>/dev/null | head -n1)"
if bash -c '. ~/.bashrc >/dev/null 2>&1; command -v brew' >/dev/null 2>&1; then echo brew_path=yes; else echo brew_path=no; fi
if [ -x "$brew" ]; then
  eval "$("$brew" shellenv)"; export HOMEBREW_NO_AUTO_UPDATE=1
  missing=''
  for t in $taps_in; do n="${t%%=*}"; brew tap 2>/dev/null </dev/null | grep -qixF -- "$n" || missing="$missing tap:$n"; done
  for p in $pkgs_in; do brew list --formula --versions "${p##*/}" >/dev/null 2>&1 </dev/null || missing="$missing $p"; done
  if [ -n "$trust_in" ]; then
    trusted=$(brew trust --json=v1 2>/dev/null </dev/null)
    for t in $trust_in; do
      case "$t" in
        tap:*|formula:*|cask:*|command:*) kind="${t%%:*}"; name="${t#*:}" ;;
        */*/*) kind=formula; name="$t" ;;
        *) kind=tap; name="$t" ;;
      esac
      key=$(case "$kind" in tap) echo taps ;; formula) echo formulae ;; cask) echo casks ;; command) echo commands ;; esac)
      jq -e --arg k "$key" --arg n "$name" '(.[$k] // []) | map(ascii_downcase) | index($n | ascii_downcase)' <<<"$trusted" >/dev/null 2>&1 ||
        missing="$missing trust:$name"
      # brew trust accepts any name: a trusted tap that is not tapped is likely a typo
      if [ "$kind" = tap ] && ! brew tap 2>/dev/null </dev/null | grep -qixF -- "$name"; then missing="$missing untapped:$name"; fi
    done
  fi
  echo "brew_missing=${missing# }"
  if [ -n "$brewfile" ] && [ -f "$brewfile" ]; then
    if brew bundle check --no-upgrade --file="$brewfile" >/dev/null 2>&1 </dev/null; then echo brewfile=ok; else echo brewfile=missing; fi
  fi
fi
if [ -d "$dir/.git" ]; then
  echo "repo=$(git -C "$dir" log --oneline -1 2>/dev/null || echo '(no commits yet)')"
  echo "repo_changes=$(git -C "$dir" -c status.showUntrackedFiles=all status --porcelain --untracked-files=all --ignore-submodules=none | wc -l)"
fi
if [ -f "${VIPER_REBOOT_FILE:-/var/run/reboot-required}" ]; then echo reboot=required; fi
echo "sshd=$(sudo sshd -T 2>/dev/null | grep -iE '^(passwordauthentication|permitrootlogin|authenticationmethods) ' | sort | tr '\n' ';')"
echo "mem=$(free -m | awk '/^Mem:/ {print $7 " MB available of " $2 " MB"}')"
echo "disk=$(df -h / | awk 'NR == 2 {print $4 " free of " $2}')"
PROBE
); then
  bad "SSH to $SSH_ALIAS failed"
  echo; echo "Check: $failures problem(s)."; exit 1
fi
val() { sed -n "s/^$1=//p" <<<"$out" | head -n1; }

case "$(val cloudinit)" in
  done) ok "cloud-init: done" ;;
  disabled) ok "cloud-init: done (disabled after the first boot)" ;;
  '')   bad "cloud-init: no status" ;;
  *)    warn "cloud-init: $(val cloudinit)" ;;
esac
if [[ "$(val cloudinit_errors)" =~ ^[1-9] ]]; then
  warn "cloud-init reported $(val cloudinit_errors) error(s) at the first boot, e.g.: $(val cloudinit_error) (see /var/log/cloud-init.log)"
fi
# the test request must really answer OK, not merely mention it in an error
answered_ok() { [[ "$(val claude_answer | tr -d '[:space:]')" =~ ^[Oo][Kk][.!]?$ ]]; }
if [[ -z "$(val claude_version)" ]]; then
  bad "Claude Code not installed"
elif [[ -n "${VIPER_ANTHROPIC_BASE_URL:-}" ]]; then
  if [[ "$(val claude_auth)" != "gateway $VIPER_CLAUDE_MODEL $VIPER_ANTHROPIC_BASE_URL" ]]; then
    bad "Claude Code $(val claude_version): not configured for $VIPER_CLAUDE_MODEL at $(redact_url "$VIPER_ANTHROPIC_BASE_URL") (has: $(val claude_auth | sed -E 's#([a-z+]+://)[^/@ ]+@#\1***@#'); rerun ./start.sh $MACHINE)"
  elif answered_ok; then
    ok "Claude Code $(val claude_version): model $VIPER_CLAUDE_MODEL at $(redact_url "$VIPER_ANTHROPIC_BASE_URL") answers"
  else
    bad "Claude Code $(val claude_version): no answer from $VIPER_CLAUDE_MODEL: $(val claude_answer)"
  fi
elif [[ -n "${VIPER_CLAUDE_TOKEN:-}" ]]; then
  if [[ "$(val claude_auth)" != token ]]; then
    bad "Claude Code $(val claude_version): token not loaded in shells (rerun ./start.sh $MACHINE)"
  elif answered_ok; then
    ok "Claude Code $(val claude_version): logged in via VIPER_CLAUDE_TOKEN and answers"
  else
    bad "Claude Code $(val claude_version): no answer with the token: $(val claude_answer)"
  fi
else
  info "Claude Code $(val claude_version): no VIPER_CLAUDE_TOKEN or VIPER_ANTHROPIC_BASE_URL, log in with claude (/login)"
fi
if [[ -z "$(val gh_version)" ]]; then
  bad "gh not installed"
elif [[ "$(val gh_auth)" == yes ]]; then
  ok "$(val gh_version): logged in"
elif [[ -n "${VIPER_GH_TOKEN:-}" ]]; then
  bad "$(val gh_version): not logged in although VIPER_GH_TOKEN is set (rerun ./start.sh $MACHINE)"
else
  info "$(val gh_version): not logged in (no VIPER_GH_TOKEN)"
fi
if [[ -n "$(val git_name)" && -n "$(val git_email)" ]]; then
  ok "git commits as $(val git_name) <$(val git_email)>"
else
  warn "git user.name/user.email are not set on the server: commits fail (VIPER_GIT_USER_NAME, VIPER_GIT_USER_EMAIL, then rerun ./start.sh $MACHINE)"
fi
if [[ "$(val docker)" == ok ]]; then ok "docker running"; else bad "docker not running"; fi
if [[ "$VIPER_SWAP" != 0 ]]; then
  swap_mb=$(val swap_mb)
  if (( ${swap_mb:-0} > 0 )); then ok "swap active ($swap_mb MB)"; else bad "swap not active (VIPER_SWAP=$VIPER_SWAP; rerun ./start.sh $MACHINE)"; fi
fi
if [[ "$VIPER_CODE_SERVER" == 1 ]]; then
  if [[ -z "$(val code_server_version)" ]]; then
    bad "code-server not installed (rerun ./start.sh $MACHINE; log: ~/.config/viper/code-server-install.log)"
  elif [[ "$(val code_server_active)" != active || "$(val code_server_http)" != yes ]]; then
    bad "code-server $(val code_server_version) is not running (rerun ./start.sh $MACHINE; journalctl -u code-server@builder)"
  else
    ok "code-server $(val code_server_version) on 127.0.0.1:$CODE_SERVER_PORT (tunnel: ssh -L $CODE_SERVER_PORT:127.0.0.1:$CODE_SERVER_PORT $SSH_ALIAS)"
  fi
fi
if [[ "$VIPER_HOMEBREW" == 1 ]]; then
  if [[ -z "$(val brew_version)" ]]; then
    bad "Homebrew not installed (rerun ./start.sh $MACHINE; log: ~/.config/viper/homebrew-install.log)"
  elif [[ "$(val brew_path)" != yes ]]; then
    bad "$(val brew_version) installed but not in the shells' PATH (rerun ./start.sh $MACHINE)"
  else
    ok "$(val brew_version)"
    if [[ -n "$(val brew_missing)" ]]; then
      bad "Homebrew: missing $(val brew_missing) (rerun ./start.sh $MACHINE; log: ~/.config/viper/brew-packages.log)"
    elif [[ -n "${VIPER_BREW_TAPS:-}${VIPER_BREW_TRUST:-}${VIPER_BREW_PACKAGES:-}" ]]; then
      read -r -d '' -a shown_taps <<<"${VIPER_BREW_TAPS:-}" || true
      ok "Homebrew: taps, trust entries and formulae present ($( (( ${#shown_taps[@]} )) && redact_taps "${shown_taps[@]}")${VIPER_BREW_TAPS:+; }${VIPER_BREW_TRUST:+trust }${VIPER_BREW_TRUST:-}${VIPER_BREW_TRUST:+; }${VIPER_BREW_PACKAGES:-})"
    fi
    case "$(val brewfile)" in
      ok) ok "Homebrew: Brewfile of the repository satisfied" ;;
      missing) warn "Homebrew: Brewfile of the repository not fully installed (rerun ./start.sh $MACHINE)" ;;
    esac
  fi
fi
if [[ -n "${REPO_URL:-}" ]]; then
  if [[ -n "$(val repo)" ]]; then
    ok "repository $WORKDIR at $(val repo) ($(val repo_changes) uncommitted changes)"
  else
    bad "repository missing: $WORKDIR"
  fi
fi
if [[ "$(val sshd)" == "authenticationmethods publickey;passwordauthentication no;permitrootlogin no;" ]]; then
  ok "sshd: public key only, no password, no root login"
else
  bad "sshd hardening not in effect: $(val sshd)"
fi
[[ "$(val reboot)" != required ]] || warn "updates need a reboot: make ssh MACHINE=$MACHINE CMD='sudo reboot' (tmux sessions end)"
info "memory: $(val mem); disk: $(val disk)"

echo
if (( failures > 0 )); then
  echo "Check: $failures problem(s)."
  final_event "check: $failures problem(s)"
  exit 1
fi
final_event "check OK"
echo 'Check OK.'
