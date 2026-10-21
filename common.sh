#!/usr/bin/env bash
# Usage (sourced): MACHINE=<name> source common.sh
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
[[ -f .env.local ]] || { echo 'Copy .env.local.example to .env.local' >&2; exit 1; }
set -a; source ./.env.local
: "${MACHINE:?Machine name required (e.g. ./start.sh <machine>)}"
[[ "$MACHINE" =~ ^[a-z0-9]([a-z0-9-]*[a-z0-9])?$ ]] || { echo 'Machine name: lowercase letters, digits, hyphens' >&2; exit 1; }
# Optional per-machine overrides (REPO_URL, SERVER_TYPE, ...)
[[ -f "machines/$MACHINE.env.local" ]] && source "./machines/$MACHINE.env.local"
set +a
# Check all required settings up front (before anything is created or deleted) and
# report every missing one at once; settings with a default get the one from .env.local.example.
missing=()
for v in DNS_DOMAIN HCLOUD_TOKEN CLOUDFLARE_API_TOKEN CLOUDFLARE_ZONE_ID; do
  [[ -n "${!v:-}" ]] || missing+=("$v")
done
(( ${#missing[@]} == 0 )) || { echo "Missing settings: ${missing[*]} (in .env.local, machines/$MACHINE.env.local or the shell)" >&2; exit 1; }
[[ "$DNS_DOMAIN" =~ ^([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,63}$ ]] || { echo "DNS_DOMAIN must be a domain like example.com (lowercase, no scheme, no trailing dot): $DNS_DOMAIN" >&2; exit 1; }
for cmd in hcloud jq curl ssh ssh-keygen python3; do command -v "$cmd" >/dev/null || { echo "Missing: $cmd" >&2; exit 1; }; done
SSH_KEY="${SSH_KEY:-$HOME/.ssh/hetzner-build}"
SSH_KEY="${SSH_KEY/#\~/$HOME}"
SERVER_TYPE="${SERVER_TYPE:-cx53}"
SERVER_LOCATION="${SERVER_LOCATION:-fsn1}"
SERVER_IMAGE="${SERVER_IMAGE:-ubuntu-26.04}"
WORKSPACE_DIR="${WORKSPACE_DIR:-/Development}"
SERVER_PREFIX="${SERVER_PREFIX:-claude}"
[[ "$SERVER_PREFIX" =~ ^[a-z0-9]([a-z0-9-]*[a-z0-9])?$ ]] || { echo "SERVER_PREFIX: lowercase letters, digits, hyphens: $SERVER_PREFIX" >&2; exit 1; }
SERVER_NAME="$SERVER_PREFIX-$MACHINE"
(( ${#SERVER_NAME} <= 63 )) || { echo "Server name $SERVER_NAME is longer than 63 characters" >&2; exit 1; }
SSH_ALIAS="hetzner-$SERVER_NAME"
# Everything per machine is keyed by SERVER_NAME (prefix + machine), so equal machine
# names under different prefixes never share a DNS record or local files.
DNS_HOST="$SERVER_NAME.$DNS_DOMAIN"
# Shared by all machines
KEY_NAME="$SERVER_PREFIX-login"
FW_NAME="$SERVER_PREFIX-ssh"
STATE="$HOME/.local/state/hetzner-claude"
mkdir -p "$STATE" "$HOME/.ssh"
chmod 700 "$STATE" "$HOME/.ssh"
# The scripts always pass this file with -F, so the user's ~/.ssh/config cannot interfere.
SSH_CFG="$HOME/.ssh/config_hetzner_$SERVER_NAME"
# Host key of this machine's server, pinned at creation.
KNOWN_PIN="$HOME/.ssh/known_hosts_hetzner_$SERVER_NAME"
IP_STATE="$STATE/ip-$SERVER_NAME"
# Hetzner ID of the server the pin belongs to; a different ID means a new server.
ID_STATE="$STATE/id-$SERVER_NAME"
# Workspace directory the server was started with (stop.sh must check that one).
WORKSPACE_STATE="$STATE/workspace-$SERVER_NAME"

# Connect by IP (the exact server we act on, independent of DNS). Trust only the host key
# pinned for this very server: a key leaked from an earlier server (it is readable in the
# user-data) is rejected, even though its certificate from the same CA is still valid.
# Without a pin (server created elsewhere) fall back to the host CA.
write_ssh_config() {
  local ip="$1" known="$KNOWN_PIN"
  [[ -s "$KNOWN_PIN" ]] || known="$HOME/.ssh/known_hosts_hetzner_claude_ca"
  cat > "$SSH_CFG" <<CFG
Host $SSH_ALIAS
    HostName $ip
    HostKeyAlias $DNS_HOST
    User builder
    IdentityFile $SSH_KEY
    IdentitiesOnly yes
    StrictHostKeyChecking yes
    CheckHostIP no
    UserKnownHostsFile $known
    HostKeyAlgorithms ssh-ed25519-cert-v01@openssh.com,ssh-ed25519
    ServerAliveInterval 15
    ServerAliveCountMax 4
CFG
  chmod 600 "$SSH_CFG"
}

# server_id: Hetzner ID of $SERVER_NAME (empty if absent); hcloud errors abort.
server_id() {
  local rows
  rows=$(hcloud server list -o noheader -o columns=name,id) || { echo 'hcloud server list failed (check HCLOUD_TOKEN / project / network)' >&2; exit 1; }
  awk -v n="$SERVER_NAME" '$1 == n {print $2; exit}' <<<"$rows"
}

# forget_stale_pin <id>: a pin recorded for another server ID is dropped (the name was
# reused by a new server, e.g. created from another computer).
forget_stale_pin() {
  local recorded
  recorded=$(cat "$ID_STATE" 2>/dev/null || true)
  if [[ -n "$recorded" && "$recorded" != "$1" && -f "$KNOWN_PIN" ]]; then
    echo "Server $SERVER_NAME was recreated (ID $recorded -> $1); dropping its old pinned host key."
    rm -f "$KNOWN_PIN"
  fi
}

# pin_from_server <id>: without a pin, the connection was verified through the host CA;
# record the server's host key read over that verified channel as the pin.
pin_from_server() {
  [[ ! -s "$KNOWN_PIN" ]] || return 0
  local key
  key=$(ssh -n -F "$SSH_CFG" -o BatchMode=yes "$SSH_ALIAS" 'cat /etc/ssh/ssh_host_ed25519_key.pub' | cut -d' ' -f1,2) || return 0
  [[ "$key" == ssh-ed25519\ * ]] || return 0
  printf '%s %s\n' "$DNS_HOST" "$key" > "$KNOWN_PIN"
  chmod 600 "$KNOWN_PIN"
  echo "$1" > "$ID_STATE"
  echo "Pinned host key of $SERVER_NAME (verified via host CA)."
}

# ssh_include add|remove: edit ~/.ssh/config in place (keeps a symlink to dotfiles),
# serialized with a lock so parallel runs do not lose each other's Include line.
ssh_include() {
  local cfg line="Include $SSH_CFG" tmp
  touch "$HOME/.ssh/config"
  cfg=$(readlink -f "$HOME/.ssh/config")
  exec 9>"$STATE/ssh-config.lock"
  if command -v flock >/dev/null; then flock 9; fi
  tmp=$(mktemp)
  if [[ "$1" == add ]]; then
    grep -Fqx "$line" "$cfg" || { { echo "$line"; cat "$cfg"; } > "$tmp"; cat "$tmp" > "$cfg"; }
  else
    grep -Fvx "$line" "$cfg" > "$tmp" || true
    cat "$tmp" > "$cfg"
  fi
  rm -f "$tmp"
  chmod 600 "$cfg"
  exec 9>&-
}

# ssh_failure_kind <stderr>: "hostkey" if ssh rejected the server's identity, else "network".
ssh_failure_kind() {
  if grep -qiE 'host key verification failed|remote host identification has changed|host key for .* has changed|no .* host key is known|certificate invalid' <<<"$1"; then
    echo hostkey
  else
    echo network
  fi
}

# hcloud_has <ssh-key|firewall|server> <name>: true if it exists. Any hcloud error
# (token, project, network) aborts instead of being mistaken for "does not exist".
hcloud_has() {
  local names
  names=$(hcloud "$1" list -o noheader -o columns=name) || { echo "hcloud $1 list failed (check HCLOUD_TOKEN / project / network)" >&2; exit 1; }
  grep -Fqx -- "$2" <<<"$names"
}
