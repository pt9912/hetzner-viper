#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
[[ -f .env.local ]] || cp .env.local.example .env.local
source ./.env.local
: "${DNS_DOMAIN:?Set DNS_DOMAIN in .env.local}"
SSH_KEY="${SSH_KEY:-$HOME/.ssh/hetzner-build}"
SSH_KEY="${SSH_KEY/#\~/$HOME}"
mkdir -p "$(dirname "$SSH_KEY")" "$HOME/.ssh"
[[ -f "$SSH_KEY" ]] || ssh-keygen -t ed25519 -f "$SSH_KEY" -N ''
CA="$HOME/.ssh/hetzner_claude_host_ca"
[[ -f "$CA" ]] || ssh-keygen -t ed25519 -f "$CA" -N '' -C 'Hetzner Claude Host CA'
KNOWN="$HOME/.ssh/known_hosts_hetzner_claude_ca"
printf '@cert-authority *.%s %s\n' "$DNS_DOMAIN" "$(cat "$CA.pub")" > "$KNOWN"
chmod 600 "$KNOWN"
cat <<MSG
Setup complete. Export credentials in your shell:
  export HCLOUD_TOKEN='...'
  export CLOUDFLARE_API_TOKEN='...'
  export CLOUDFLARE_ZONE_ID='...'
Then run ./start.sh <machine> [repo-url]
Host CA public key for iPad SSH clients (only if they support OpenSSH host CAs):
$(cat "$CA.pub")
MSG
