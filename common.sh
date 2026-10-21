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
: "${DNS_DOMAIN:?Set DNS_DOMAIN in .env.local}" "${HCLOUD_TOKEN:?Set HCLOUD_TOKEN}" "${CLOUDFLARE_API_TOKEN:?Set CLOUDFLARE_API_TOKEN}"
for cmd in hcloud jq curl ssh ssh-keygen python3; do command -v "$cmd" >/dev/null || { echo "Missing: $cmd" >&2; exit 1; }; done
SSH_KEY="${SSH_KEY/#\~/$HOME}"
WORKSPACE_DIR="${WORKSPACE_DIR:-/Development}"
SERVER_PREFIX="${SERVER_PREFIX:-claude}"
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
CFG
  chmod 600 "$SSH_CFG"
}

# hcloud_has <ssh-key|firewall|server> <name>: true if it exists. Any hcloud error
# (token, project, network) aborts instead of being mistaken for "does not exist".
hcloud_has() {
  local names
  names=$(hcloud "$1" list -o noheader -o columns=name) || { echo "hcloud $1 list failed (check HCLOUD_TOKEN / project / network)" >&2; exit 1; }
  grep -Fqx -- "$2" <<<"$names"
}
