#!/usr/bin/env bash
# Read-only checks of a running machine: DNS, access like another device (host CA + DNS
# name, as an iPad or a second computer sees it) and the state of the server.
# Usage: ./check.sh <machine>
set -euo pipefail
MACHINE="${1:?Usage: ./check.sh <machine>}"; export MACHINE
source "$(dirname "$0")/common.sh"

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
if ! out=$(ssh -F "$SSH_CFG" "$SSH_ALIAS" "bash -s -- $(printf '%q ' "$WORKDIR" "${VIPER_CLAUDE_TOKEN:+1}${VIPER_ANTHROPIC_BASE_URL:+1}")" <<'PROBE'
set -uo pipefail
dir="$1"; ask_claude="$2"
export LC_ALL=C PATH="$HOME/.local/bin:$PATH"
echo "cloudinit=$(cloud-init status 2>/dev/null | sed -n 's/^status: //p')"
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
if docker info >/dev/null 2>&1; then echo docker=ok; else echo docker=no; fi
if [ -d "$dir/.git" ]; then
  echo "repo=$(git -C "$dir" log --oneline -1 2>/dev/null || echo '(no commits yet)')"
  echo "repo_changes=$(git -C "$dir" status --short | wc -l)"
fi
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
  '')   bad "cloud-init: no status" ;;
  *)    warn "cloud-init: $(val cloudinit)" ;;
esac
if [[ -z "$(val claude_version)" ]]; then
  bad "Claude Code not installed"
elif [[ -n "${VIPER_ANTHROPIC_BASE_URL:-}" ]]; then
  if [[ "$(val claude_auth)" != "gateway $VIPER_CLAUDE_MODEL $VIPER_ANTHROPIC_BASE_URL" ]]; then
    bad "Claude Code $(val claude_version): not configured for $VIPER_CLAUDE_MODEL at $VIPER_ANTHROPIC_BASE_URL (has: $(val claude_auth); rerun ./start.sh $MACHINE)"
  elif [[ "$(val claude_answer)" == *OK* ]]; then
    ok "Claude Code $(val claude_version): model $VIPER_CLAUDE_MODEL at $VIPER_ANTHROPIC_BASE_URL answers"
  else
    bad "Claude Code $(val claude_version): no answer from $VIPER_CLAUDE_MODEL: $(val claude_answer)"
  fi
elif [[ -n "${VIPER_CLAUDE_TOKEN:-}" ]]; then
  if [[ "$(val claude_auth)" != token ]]; then
    bad "Claude Code $(val claude_version): token not loaded in shells (rerun ./start.sh $MACHINE)"
  elif [[ "$(val claude_answer)" == *OK* ]]; then
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
if [[ "$(val docker)" == ok ]]; then ok "docker running"; else bad "docker not running"; fi
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
info "memory: $(val mem); disk: $(val disk)"

echo
if (( failures > 0 )); then
  echo "Check: $failures problem(s)."
  exit 1
fi
echo 'Check OK.'
