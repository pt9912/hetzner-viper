#!/usr/bin/env bash
set -euo pipefail
MACHINE="${1:?Usage: ./start.sh <machine> [repo-url]}"; export MACHINE
REPO_URL_ARG="${2:-}"
source "$(dirname "$0")/common.sh"
[[ -z "$REPO_URL_ARG" ]] || REPO_URL="$REPO_URL_ARG"
# read-only checks first, so a wrong setting fails before anything is created
if ! preflight=$(./preflight.sh "$MACHINE" "$REPO_URL_ARG"); then
  echo "$preflight" >&2
  exit 1
fi
echo "$preflight"
CA="$HOME/.ssh/hetzner_claude_host_ca"
[[ -f "$CA" && -f "$SSH_KEY.pub" ]] || { echo 'Run ./setup.sh first' >&2; exit 1; }
GIT_DEPLOY_KEY_FILE="${GIT_DEPLOY_KEY_FILE:+${GIT_DEPLOY_KEY_FILE/#\~/$HOME}}"
IPAD_SSH_PUBLIC_KEY_FILE="${IPAD_SSH_PUBLIC_KEY_FILE:+${IPAD_SSH_PUBLIC_KEY_FILE/#\~/$HOME}}"
for f in "${GIT_DEPLOY_KEY_FILE:-}" "${IPAD_SSH_PUBLIC_KEY_FILE:-}"; do
  [[ -z "$f" || -r "$f" ]] || { echo "File not readable: $f (in the container only ~/.ssh is visible)" >&2; exit 1; }
done
# Hetzner allows each public key only once per project: reuse it under whatever name it has.
fingerprint=$(ssh-keygen -E md5 -lf "$SSH_KEY.pub" | awk '{print $2}' | sed 's/^MD5://')
# fingerprint first: names may contain spaces, fingerprints do not
keys=$(hcloud ssh-key list -o noheader -o columns=fingerprint,name) || { echo 'hcloud ssh-key list failed (check HCLOUD_TOKEN / project / network)' >&2; exit 1; }
key_names() { awk -v fp="$1" '{ f = $1; sub(/^[^ ]+ +/, ""); sub(/ +$/, ""); if (fp == "" || tolower(f) == tolower(fp)) print }' <<<"$keys"; }
existing=$(key_names "$fingerprint" | head -n1)
if [[ -n "$existing" ]]; then
  KEY_NAME="$existing"
elif key_names '' | grep -Fqx -- "$KEY_NAME"; then
  echo "SSH key $KEY_NAME in Hetzner belongs to a different public key than $SSH_KEY.pub" >&2; exit 1
else
  hcloud ssh-key create --name "$KEY_NAME" --public-key-from-file "$SSH_KEY.pub"
fi
if ! hcloud_has firewall "$FW_NAME"; then
  hcloud firewall create --name "$FW_NAME"
fi
# Inbound SSH (TCP 22) and ICMP (ping, for diagnosis) from SSH_ALLOWED_CIDRS; a missing rule
# is added (also to an existing firewall), other rules are left alone.
# separate assignment: a failing describe must abort, not count as "has rules"
rules=$(hcloud firewall describe "$FW_NAME" -o json | jq -c '.rules')
has_rule() { jq -e --arg p "$1" --arg port "${2:-}" 'any(.[]; .direction == "in" and .protocol == $p and (($port == "") or .port == $port))' <<<"$rules" >/dev/null; }
cidrs="${SSH_ALLOWED_CIDRS:-0.0.0.0/0,::/0}"
has_rule tcp 22 || hcloud firewall add-rule "$FW_NAME" --direction in --protocol tcp --port 22 --source-ips "$cidrs"
has_rule icmp || hcloud firewall add-rule "$FW_NAME" --direction in --protocol icmp --source-ips "$cidrs"
if has_rule tcp 22 && has_rule icmp; then
  echo "Existing firewall $FW_NAME: verify that its rules permit your connection."
fi
if ! hcloud_has server "$SERVER_NAME"; then
  TMP=$(mktemp -d)
  chmod 700 "$TMP"
  trap 'rm -rf "$TMP"' EXIT
  # -C: the default comment (local user@host) would end up in the user-data
  ssh-keygen -q -t ed25519 -N '' -C "$SERVER_NAME" -f "$TMP/ssh_host_ed25519_key"
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
  SERVER_ID=$(server_id)
  echo "$SERVER_ID" > "$ID_STATE"
  # a new server has none of the workspaces an earlier one with this name had
  rm -f "$WORKSPACE_STATE"
  REUSED=0
else
  echo 'Existing server: reusing it.'
  SERVER_ID=$(server_id)
  forget_stale_pin "$SERVER_ID"
  REUSED=1
fi
IP=$(hcloud server ip "$SERVER_NAME")
echo "$IP" > "$IP_STATE"
write_ssh_config "$IP"
ssh_include add || ssh_include_warning
# The DNS name is for people (iPad, other clients); the scripts connect by IP and need not wait for it.
./dns.sh set "$IP"
echo 'Waiting for SSH and cloud-init ...'
# The probe always exits 0 once connected; only connection failures are retried.
# cloud-init exits 0 (done), 2 (done with warnings) or 1 (errors).
probe='cloud-init status --wait >/dev/null 2>&1; echo "cloud-init=$?"; test -f /var/tmp/claude-ready && echo claude=ok; docker info >/dev/null 2>&1 && echo docker=ok; true'
# While a new server boots, sshd may briefly present a key other than the provisioned one
# and builder may not exist yet, so host-key and auth errors are retried too. A reused
# server is already up: those errors are permanent there and end the wait at once.
status=''; err=''
for i in $(seq 1 120); do
  if status=$(ssh -n -F "$SSH_CFG" -o BatchMode=yes -o ConnectTimeout=5 "$SSH_ALIAS" "$probe" 2>"$STATE/ssh-err-$SERVER_NAME"); then
    break
  fi
  status=''; err=$(cat "$STATE/ssh-err-$SERVER_NAME")
  (( REUSED )) && [[ "$(ssh_failure_kind "$err")" != network ]] && break
  sleep 5
done
rm -f "$STATE/ssh-err-$SERVER_NAME"
if [[ -z "$status" ]]; then
  if [[ "$(ssh_failure_kind "$err")" == hostkey ]]; then
    echo "SSH: the server's host key is not trusted; server retained." >&2
    echo "  $err" >&2
    echo "  The server answers but does not prove to be $SERVER_NAME (stale pin, expired certificate without pin, or someone in between)." >&2
  elif [[ "$(ssh_failure_kind "$err")" == auth ]]; then
    echo "SSH: the server rejects the login key $SSH_KEY ($err); server retained." >&2
    echo "  Was the server created with another login key? Its authorized key is set at creation." >&2
  else
    echo 'SSH not reachable; server retained.' >&2
  fi
  exit 1
fi
# Verified via the host CA (no pin yet): pin the key from now on.
if [[ ! -s "$KNOWN_PIN" ]]; then
  pin_from_server "$SERVER_ID"
  write_ssh_config "$IP"
fi
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
# remember every workspace this server was started with: stop.sh checks all of them
grep -qxF -- "$WORKSPACE_DIR" "$WORKSPACE_STATE" 2>/dev/null || echo "$WORKSPACE_DIR" >> "$WORKSPACE_STATE"
# GitHub CLI login for builder. The token goes through stdin (not argv, not user-data);
# `gh auth setup-git` makes git use it for HTTPS clone/push. A dedicated variable, so a
# GH_TOKEN the user exported for local gh is never copied to a server.
if [[ -n "${VIPER_GH_TOKEN:-}" ]]; then
  host="${VIPER_GH_HOST:-github.com}"
  gh_host=$(printf %q "$host")
  # SSH URLs of that host go over HTTPS with the token: the server has no SSH key for it
  https=$(printf %q "https://$host/"); scp_form=$(printf %q "git@$host:"); ssh_form=$(printf %q "ssh://git@$host/")
  ssh -F "$SSH_CFG" "$SSH_ALIAS" "gh auth login --hostname $gh_host --with-token && gh auth setup-git --hostname $gh_host &&
    { git config --global --unset-all url.$https.insteadOf || true; } &&
    git config --global --add url.$https.insteadOf $scp_form && git config --global --add url.$https.insteadOf $ssh_form" <<<"$VIPER_GH_TOKEN"
  echo "gh: logged in to $host (SSH URLs of $host are cloned over HTTPS)"
fi
# Claude Code login for builder with a long-lived subscription token (`claude setup-token`).
# The token goes through stdin into a 600 file; a line at the very top of .bashrc/.profile
# (before the "not interactive: return" guard) loads it for ssh commands, tmux and VS Code.
# A dedicated variable, so a CLAUDE_CODE_OAUTH_TOKEN exported locally is never copied.
if [[ -n "${VIPER_CLAUDE_TOKEN:-}" ]]; then
  claude_login='umask 077 && mkdir -p ~/.config/viper && IFS= read -r t &&
    printf "export CLAUDE_CODE_OAUTH_TOKEN=%q\n" "$t" > ~/.config/viper/claude.env &&
    line="[ -f ~/.config/viper/claude.env ] && . ~/.config/viper/claude.env" &&
    for f in ~/.bashrc ~/.profile; do
      grep -qxF "$line" "$f" 2>/dev/null || { { echo "$line"; [ ! -f "$f" ] || cat "$f"; } > "$f.viper" && mv "$f.viper" "$f"; }
    done'
  ssh -F "$SSH_CFG" "$SSH_ALIAS" "$claude_login" <<<"$VIPER_CLAUDE_TOKEN"
  echo 'claude: logged in with VIPER_CLAUDE_TOKEN'
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
  git clone ${branch:+--branch "$branch"} -- "$url" "$dir"
fi
[[ -z "$use_key" ]] || git -C "$dir" config core.sshCommand "$GIT_SSH_COMMAND"
CLONE
fi
echo "Ready: ssh $SSH_ALIAS (work dir: $WORKDIR)"
# VS Code and its extensions must not inherit the API tokens from .env.local
if command -v code >/dev/null; then
  env -u HCLOUD_TOKEN -u CLOUDFLARE_API_TOKEN -u CLOUDFLARE_ZONE_ID -u VIPER_GH_TOKEN -u GH_TOKEN \
    -u VIPER_CLAUDE_TOKEN -u CLAUDE_CODE_OAUTH_TOKEN code --remote "ssh-remote+$SSH_ALIAS" "$WORKDIR" || true
fi
