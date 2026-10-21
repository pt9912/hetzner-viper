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
# Hetzner allows each public key only once per project: reuse it under whatever name it has.
fingerprint=$(ssh-keygen -E md5 -lf "$SSH_KEY.pub" | awk '{print $2}' | sed 's/^MD5://')
keys=$(hcloud ssh-key list -o noheader -o columns=name,fingerprint) || { echo 'hcloud ssh-key list failed (check HCLOUD_TOKEN / project / network)' >&2; exit 1; }
existing=$(awk -v fp="$fingerprint" 'tolower($2) == tolower(fp) {print $1; exit}' <<<"$keys")
if [[ -n "$existing" ]]; then
  KEY_NAME="$existing"
elif awk -v n="$KEY_NAME" '$1 == n {found=1} END {exit !found}' <<<"$keys"; then
  echo "SSH key $KEY_NAME in Hetzner belongs to a different public key than $SSH_KEY.pub" >&2; exit 1
else
  hcloud ssh-key create --name "$KEY_NAME" --public-key-from-file "$SSH_KEY.pub"
fi
if ! hcloud_has firewall "$FW_NAME"; then
  hcloud firewall create --name "$FW_NAME"
fi
# separate assignment: a failing describe must abort, not count as "has rules"
rule_count=$(hcloud firewall describe "$FW_NAME" -o json | jq '.rules | length')
if [[ "$rule_count" == 0 ]]; then
  hcloud firewall add-rule "$FW_NAME" --direction in --protocol tcp --port 22 --source-ips "${SSH_ALLOWED_CIDRS:-0.0.0.0/0,::/0}"
else
  echo "Existing firewall $FW_NAME: verify that its rules permit your connection."
fi
if ! hcloud_has server "$SERVER_NAME"; then
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
  printf '%s %s\n' "$DNS_HOST" "$(cut -d' ' -f1,2 "$TMP/ssh_host_ed25519_key.pub")" > "$KNOWN_PIN"
  chmod 600 "$KNOWN_PIN"
  hcloud server create --name "$SERVER_NAME" --type "$SERVER_TYPE" --location "$SERVER_LOCATION" \
    --image "$SERVER_IMAGE" --ssh-key "$KEY_NAME" --firewall "$FW_NAME" \
    --user-data-from-file "$TMP/cloud-init.yaml"
else
  echo 'Existing server: reusing it (trusted via host CA, or the host key pinned at creation).'
fi
IP=$(hcloud server ip "$SERVER_NAME")
echo "$IP" > "$IP_STATE"
write_ssh_config "$IP"
touch "$HOME/.ssh/config"
chmod 600 "$HOME/.ssh/config"
if ! grep -Fqx "Include $SSH_CFG" "$HOME/.ssh/config"; then
  { echo "Include $SSH_CFG"; cat "$HOME/.ssh/config"; } > "$HOME/.ssh/config.new" && mv "$HOME/.ssh/config.new" "$HOME/.ssh/config"
  chmod 600 "$HOME/.ssh/config"
fi
# The DNS name is for people (iPad, other clients); the scripts connect by IP and need not wait for it.
./dns.sh set "$IP"
echo 'Waiting for SSH and cloud-init ...'
# The probe always exits 0 once connected; only connection failures are retried.
# cloud-init exits 0 (done), 2 (done with warnings) or 1 (errors).
probe='cloud-init status --wait >/dev/null 2>&1; echo "cloud-init=$?"; test -f /var/tmp/claude-ready && echo claude=ok; docker info >/dev/null 2>&1 && echo docker=ok; true'
status=''
for i in $(seq 1 120); do
  status=$(ssh -n -F "$SSH_CFG" -o BatchMode=yes -o ConnectTimeout=5 "$SSH_ALIAS" "$probe" 2>/dev/null) && break
  status=''
  sleep 5
done
[[ -n "$status" ]] || { echo 'SSH not reachable; server retained.' >&2; exit 1; }
# runcmd runs only once per server; a failed Claude install is repaired here instead.
if ! grep -qx 'claude=ok' <<<"$status"; then
  echo 'Claude Code is not installed yet; installing ...'
  ssh -n -F "$SSH_CFG" "$SSH_ALIAS" "bash -o pipefail -c 'curl -fsSL https://claude.ai/install.sh | bash' && sudo touch /var/tmp/claude-ready" \
    || { echo 'Claude Code installation failed; server retained. Rerun ./start.sh to try again.' >&2; exit 1; }
fi
grep -qx 'docker=ok' <<<"$status" || { echo 'Docker is not running; see /var/log/cloud-init-output.log on the server. Server retained.' >&2; exit 1; }
grep -qx 'cloud-init=1' <<<"$status" && echo 'WARNING: cloud-init reported errors; see /var/log/cloud-init-output.log on the server.' >&2
# Work directory on the server's own disk; it is deleted together with the server.
ssh -n -F "$SSH_CFG" "$SSH_ALIAS" "sudo install -d -o builder -g builder $(printf %q "$WORKSPACE_DIR")"
echo "$WORKSPACE_DIR" > "$WORKSPACE_STATE"
# GitHub CLI login for builder. The token goes through stdin (not argv, not user-data);
# `gh auth setup-git` makes git use it for HTTPS clone/push.
if [[ -n "${GH_TOKEN:-}" ]]; then
  gh_host=$(printf %q "${GH_HOST:-github.com}")
  ssh -F "$SSH_CFG" "$SSH_ALIAS" "gh auth login --hostname $gh_host --with-token && gh auth setup-git --hostname $gh_host" <<<"$GH_TOKEN"
  echo "gh: logged in to ${GH_HOST:-github.com}"
fi
WORKDIR="$WORKSPACE_DIR"
if [[ -n "${REPO_URL:-}" ]]; then
  WORKDIR="$WORKSPACE_DIR/$(basename "${REPO_URL%.git}")"
  if [[ -n "${GIT_DEPLOY_KEY_FILE:-}" ]]; then
    scp -F "$SSH_CFG" -q "$GIT_DEPLOY_KEY_FILE" "$SSH_ALIAS:.ssh/deploy_key"
    ssh -n -F "$SSH_CFG" "$SSH_ALIAS" 'chmod 600 ~/.ssh/deploy_key'
  fi
  # ssh passes one command string to the remote shell: quote each argument, keep empty ones
  ssh -F "$SSH_CFG" "$SSH_ALIAS" "bash -s -- $(printf '%q ' "$REPO_URL" "$WORKDIR" "${REPO_BRANCH:-}" "${GIT_DEPLOY_KEY_FILE:+1}")" <<'CLONE'
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
# VS Code and its extensions must not inherit the API tokens from .env.local
if command -v code >/dev/null; then
  env -u HCLOUD_TOKEN -u CLOUDFLARE_API_TOKEN -u CLOUDFLARE_ZONE_ID -u GH_TOKEN code --remote "ssh-remote+$SSH_ALIAS" "$WORKDIR" || true
fi
