#!/usr/bin/env bash
set -euo pipefail
MACHINE="${1:?Usage: ./start.sh <machine> [repo-url]}"; export MACHINE
REPO_URL_ARG="${2:-}"
source "$(dirname "$0")/common.sh"
[[ -z "$REPO_URL_ARG" ]] || REPO_URL="$REPO_URL_ARG"
: "${CLOUDFLARE_ZONE_ID:?Set CLOUDFLARE_ZONE_ID}"
CA="$HOME/.ssh/hetzner_claude_host_ca"
[[ -f "$CA" && -f "$SSH_KEY.pub" ]] || { echo 'Run ./setup.sh first' >&2; exit 1; }
GIT_DEPLOY_KEY_FILE="${GIT_DEPLOY_KEY_FILE:+${GIT_DEPLOY_KEY_FILE/#\~/$HOME}}"
IPAD_SSH_PUBLIC_KEY_FILE="${IPAD_SSH_PUBLIC_KEY_FILE:+${IPAD_SSH_PUBLIC_KEY_FILE/#\~/$HOME}}"
for f in "${GIT_DEPLOY_KEY_FILE:-}" "${IPAD_SSH_PUBLIC_KEY_FILE:-}"; do
  [[ -z "$f" || -r "$f" ]] || { echo "File not readable: $f (in the container only ~/.ssh is visible)" >&2; exit 1; }
done
if ! hcloud ssh-key describe "$KEY_NAME" >/dev/null 2>&1; then
  hcloud ssh-key create --name "$KEY_NAME" --public-key-from-file "$SSH_KEY.pub"
fi
if ! hcloud firewall describe "$FW_NAME" >/dev/null 2>&1; then
  hcloud firewall create --name "$FW_NAME"
fi
if [[ "$(hcloud firewall describe "$FW_NAME" -o json | jq '.rules | length')" == 0 ]]; then
  hcloud firewall add-rule "$FW_NAME" --direction in --protocol tcp --port 22 --source-ips "${SSH_ALLOWED_CIDRS:-0.0.0.0/0,::/0}"
else
  echo "Existing firewall $FW_NAME: verify that its rules permit your connection."
fi
if ! hcloud server describe "$SERVER_NAME" >/dev/null 2>&1; then
  TMP=$(mktemp -d)
  chmod 700 "$TMP"
  trap 'rm -rf "$TMP"' EXIT
  ssh-keygen -q -t ed25519 -N '' -f "$TMP/ssh_host_ed25519_key"
  ssh-keygen -q -s "$CA" -I "$SERVER_NAME-$(date +%s)" -h -n "$DNS_HOST" -V -5m:+7d "$TMP/ssh_host_ed25519_key.pub"
  python3 - "$SSH_KEY.pub" cloud-init.yaml "$TMP/cloud-init.yaml" "$TMP" "${IPAD_SSH_PUBLIC_KEY_FILE:-}" <<'PYGEN'
import sys, json
pubfile, src, dest, tmp, ipad_pubfile = sys.argv[1:]
s = open(src).read()
key = open(pubfile).read().strip()
needle = '    lock_passwd: true\n'
assert s.count(needle) == 1
keys = [key]
if ipad_pubfile:
    keys.append(open(ipad_pubfile).read().strip())
s = s.replace(needle, needle + '    ssh_authorized_keys:\n' + ''.join('      - ' + json.dumps(k) + '\n' for k in keys))
# sshd validation and reload are already defined in the base cloud-init
def block(v, ind):
    return ''.join(' '*ind + l + '\n' for l in v.rstrip('\n').split('\n'))
rd = lambda n: open(tmp+'/'+n).read()
# cloud-init's ssh module deletes /etc/ssh/ssh_host_* on first boot, so the host key
# must be passed via ssh_keys instead of write_files.
extra = '\nssh_keys:\n  ed25519_private: |\n' + block(rd('ssh_host_ed25519_key'), 4)
extra += '  ed25519_public: ' + json.dumps(rd('ssh_host_ed25519_key.pub').strip()) + '\n'
extra += '  ed25519_certificate: ' + json.dumps(rd('ssh_host_ed25519_key-cert.pub').strip()) + '\n'
open(dest,'w').write(s+extra)
PYGEN
  chmod 600 "$TMP/cloud-init.yaml"
  hcloud server create --name "$SERVER_NAME" --type "$SERVER_TYPE" --location "$SERVER_LOCATION" \
    --image "$SERVER_IMAGE" --ssh-key "$KEY_NAME" --firewall "$FW_NAME" \
    --user-data-from-file "$TMP/cloud-init.yaml"
else
  echo 'Existing server: requires a previously CA-signed host certificate.'
fi
IP=$(hcloud server ip "$SERVER_NAME")
echo "$IP" > "$STATE/ip-$MACHINE"
write_ssh_config
touch "$HOME/.ssh/config"
chmod 600 "$HOME/.ssh/config"
if ! grep -Fqx "Include $SSH_CFG" "$HOME/.ssh/config"; then
  { echo "Include $SSH_CFG"; cat "$HOME/.ssh/config"; } > "$HOME/.ssh/config.new" && mv "$HOME/.ssh/config.new" "$HOME/.ssh/config"
  chmod 600 "$HOME/.ssh/config"
fi
./dns.sh set "$IP"
echo "Waiting for DNS: $DNS_HOST -> $IP"
RESOLVED=''
for i in $(seq 1 60); do
  RESOLVED=$(getent ahostsv4 "$DNS_HOST" 2>/dev/null | awk 'NR==1{print $1}' || true)
  [[ "$RESOLVED" == "$IP" ]] && break
  sleep 5
done
[[ "$RESOLVED" == "$IP" ]] || { echo 'DNS not yet propagated; server retained.' >&2; exit 1; }
echo 'Waiting for trusted SSH host certificate and cloud-init ...'
ready=0
for i in $(seq 1 120); do
  if ssh -F "$SSH_CFG" -o BatchMode=yes -o ConnectTimeout=5 "$SSH_ALIAS" \
    'cloud-init status --wait >/dev/null 2>&1; rc=$?; { [ $rc = 0 ] || [ $rc = 2 ]; } && test -f /var/tmp/claude-ready && docker info >/dev/null 2>&1' 2>/dev/null; then
    ready=1; break
  fi
  sleep 5
done
[[ "$ready" == 1 ]] || { echo 'SSH/cloud-init not ready; server retained.' >&2; exit 1; }
# Work directory on the server's own disk; it is deleted together with the server.
ssh -F "$SSH_CFG" "$SSH_ALIAS" "sudo install -d -o builder -g builder '$WORKSPACE_DIR'"
# GitHub CLI login for builder. The token goes through stdin (not argv, not user-data);
# `gh auth setup-git` makes git use it for HTTPS clone/push.
if [[ -n "${GH_TOKEN:-}" ]]; then
  ssh -F "$SSH_CFG" "$SSH_ALIAS" "gh auth login --hostname '${GH_HOST:-github.com}' --with-token && gh auth setup-git --hostname '${GH_HOST:-github.com}'" <<<"$GH_TOKEN"
  echo "gh: logged in to ${GH_HOST:-github.com}"
fi
WORKDIR="$WORKSPACE_DIR"
if [[ -n "${REPO_URL:-}" ]]; then
  WORKDIR="$WORKSPACE_DIR/$(basename "${REPO_URL%.git}")"
  if [[ -n "${GIT_DEPLOY_KEY_FILE:-}" ]]; then
    scp -F "$SSH_CFG" -q "$GIT_DEPLOY_KEY_FILE" "$SSH_ALIAS:.ssh/deploy_key"
    ssh -F "$SSH_CFG" "$SSH_ALIAS" 'chmod 600 ~/.ssh/deploy_key'
  fi
  ssh -F "$SSH_CFG" "$SSH_ALIAS" bash -s -- "$REPO_URL" "$WORKDIR" "${REPO_BRANCH:-}" "${GIT_DEPLOY_KEY_FILE:+1}" <<'CLONE'
set -euo pipefail
url="$1"; dir="$2"; branch="$3"; use_key="$4"
if [[ -n "$use_key" ]]; then
  # first contact with the git host is trusted on first use (accept-new)
  export GIT_SSH_COMMAND='ssh -i ~/.ssh/deploy_key -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new'
fi
if [[ -d "$dir/.git" ]]; then
  echo "Repository already present: $dir"
else
  git clone ${branch:+--branch "$branch"} "$url" "$dir"
fi
[[ -z "$use_key" ]] || git -C "$dir" config core.sshCommand "$GIT_SSH_COMMAND"
CLONE
fi
echo "Ready: ssh $SSH_ALIAS (work dir: $WORKDIR)"
if command -v code >/dev/null; then code --remote "ssh-remote+$SSH_ALIAS" "$WORKDIR" || true; fi
