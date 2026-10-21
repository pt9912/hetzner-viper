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
SSH_ALIAS="hetzner-$SERVER_NAME"
DNS_HOST="$MACHINE.$DNS_DOMAIN"
# Shared by all machines
KEY_NAME="$SERVER_PREFIX-login"
FW_NAME="$SERVER_PREFIX-ssh"
STATE="$HOME/.local/state/hetzner-claude"
mkdir -p "$STATE" "$HOME/.ssh"
chmod 700 "$STATE" "$HOME/.ssh"
# The scripts always pass this file with -F, so the user's ~/.ssh/config cannot interfere.
SSH_CFG="$HOME/.ssh/config_hetzner_claude_$MACHINE"

write_ssh_config() {
  cat > "$SSH_CFG" <<CFG
Host $SSH_ALIAS
    HostName $DNS_HOST
    User builder
    IdentityFile $SSH_KEY
    IdentitiesOnly yes
    StrictHostKeyChecking yes
    CheckHostIP no
    UserKnownHostsFile $HOME/.ssh/known_hosts_hetzner_claude_ca
    HostKeyAlgorithms ssh-ed25519-cert-v01@openssh.com
CFG
  chmod 600 "$SSH_CFG"
}
