#!/usr/bin/env bash
set -euo pipefail
MACHINE="${1:?Usage: ./start.sh <machine> [repo-url]}"; export MACHINE
REPO_URL_ARG="${2:-}"
source "$(dirname "$0")/common.sh"
start_transcript up
[[ -z "$REPO_URL_ARG" ]] || REPO_URL="$REPO_URL_ARG"
log_event "start ($SERVER_TYPE, $SERVER_LOCATION, $SERVER_IMAGE${REPO_URL:+, $(redact_url "$REPO_URL")})"
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
  EXIT_CMDS+=('rm -rf "$TMP"')
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
  log_event "server created id=$SERVER_ID"
  # a new server has none of the workspaces an earlier one with this name had
  rm -f "$WORKSPACE_STATE" "$READY_STATE"
  REUSED=0
else
  echo 'Existing server: reusing it.'
  SERVER_ID=$(server_id)
  forget_stale_pin "$SERVER_ID"
  log_event "reusing server id=$SERVER_ID"
  # "reused" means set up by a completed run; a server whose first run was interrupted may
  # still be booting (transient ssh errors, the reboot after its updates still to come)
  REUSED=0
  [[ "$(cat "$READY_STATE" 2>/dev/null)" == "$SERVER_ID" ]] && REUSED=1
fi
IP=$(hcloud server ip "$SERVER_NAME")
echo "$IP" > "$IP_STATE"
log_event "ip=$IP dns=$DNS_HOST"
write_ssh_config "$IP"
ssh_include add || ssh_include_warning
# The DNS name is for people (iPad, other clients); the scripts connect by IP and need not wait for it.
./dns.sh set "$IP"
echo 'Waiting for SSH and cloud-init ...'
# The probe always exits 0 once connected; only connection failures are retried.
# cloud-init exits 0 (done), 2 (done with warnings) or 1 (errors).
# reboot=pending: updates were installed and cloud-init is about to restart the server.
# (VIPER_REBOOT_FILE only exists for the offline tests; ssh does not forward it.)
probe='cloud-init status --wait >/dev/null 2>&1; echo "cloud-init=$?"; test -f /var/tmp/claude-ready && echo claude=ok; docker info >/dev/null 2>&1 && echo docker=ok; test -f "${VIPER_REBOOT_FILE:-/var/run/reboot-required}" && echo reboot=pending; echo "ready=$(cat ~/.config/viper/ready 2>/dev/null)"; true'
# While a new server boots, sshd may briefly present a key other than the provisioned one
# and builder may not exist yet, so host-key and auth errors are retried too. A reused
# server is already up: those errors are permanent there and end the wait at once.
status=''; err=''; announced=0; reboot_waits=0
for i in $(seq 1 120); do
  if status=$(ssh -n -F "$SSH_CFG" -o BatchMode=yes -o ConnectTimeout=5 "$SSH_ALIAS" "$probe" 2>"$STATE/ssh-err-$SERVER_NAME"); then
    # set up by a completed run (from this or another computer): the server records it itself
    [[ "$(sed -n 's/^ready=//p' <<<"$status")" == "$SERVER_ID" ]] && REUSED=1
    # A new server restarts once after its updates: wait for it instead of working into it.
    # The post-upgrade reboot follows cloud-init within a minute or so; after ~3 minutes the
    # flag is from something else (e.g. later unattended upgrades): carry on with a warning.
    if (( ! REUSED )) && grep -qx 'reboot=pending' <<<"$status" && (( ++reboot_waits <= 36 )); then
      (( announced )) || { echo 'Updates need a reboot; waiting for the server to restart ...'; announced=1; }
      status=''; err='the server did not come back after the reboot for its updates'
      sleep 5; continue
    fi
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
    echo "SSH not reachable; server retained.${err:+ Last error: $err}" >&2
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
grep -qx 'reboot=pending' <<<"$status" && echo "WARNING: updates on the server need a reboot: make ssh MACHINE=$MACHINE CMD='sudo reboot'" >&2
# Work directory on the server's own disk; it is deleted together with the server.
# Record the workspace on the server right away (also if a later step fails), so stop.sh from
# any computer checks it.
ws_q=$(printf %q "$WORKSPACE_DIR")
ssh -n -F "$SSH_CFG" "$SSH_ALIAS" "sudo install -d -o builder -g builder $ws_q && mkdir -p ~/.config/viper && { grep -qxF -- $ws_q ~/.config/viper/workspaces 2>/dev/null || printf '%s\n' $ws_q >> ~/.config/viper/workspaces; }"
# remember every workspace this server was started with (here and, below, on the server
# itself, so stop.sh from another computer knows them too): stop.sh checks all of them
grep -qxF -- "$WORKSPACE_DIR" "$WORKSPACE_STATE" 2>/dev/null || echo "$WORKSPACE_DIR" >> "$WORKSPACE_STATE"
# GitHub CLI login for builder. The token goes through stdin (not argv, not user-data);
# `gh auth setup-git` makes git use it for HTTPS clone/push. A dedicated variable, so a
# GH_TOKEN the user exported for local gh is never copied to a server.
if [[ -n "${VIPER_GH_TOKEN:-}" ]]; then
  host="${VIPER_GH_HOST:-github.com}"
  gh_host=$(printf %q "$host")
  # Without a deploy key the server has no SSH key for the git host, so SSH URLs of that host
  # go over HTTPS with the token. With a deploy key, SSH (and the key) stay in charge.
  https=$(printf %q "https://$host/"); scp_form=$(printf %q "git@$host:"); ssh_form=$(printf %q "ssh://git@$host/")
  rewrite="git config --global --add url.$https.insteadOf $scp_form && git config --global --add url.$https.insteadOf $ssh_form"
  [[ -z "${GIT_DEPLOY_KEY_FILE:-}" ]] || rewrite=true
  ssh -F "$SSH_CFG" "$SSH_ALIAS" "gh auth login --hostname $gh_host --with-token && gh auth setup-git --hostname $gh_host &&
    { git config --global --unset-all url.$https.insteadOf || true; } && $rewrite" <<<"$VIPER_GH_TOKEN"
  if [[ -n "${GIT_DEPLOY_KEY_FILE:-}" ]]; then
    echo "gh: logged in to $host (repository via the deploy key)"
  else
    echo "gh: logged in to $host (SSH URLs of $host are cloned over HTTPS)"
  fi
fi
# Claude Code login for builder, one of:
#  - VIPER_CLAUDE_TOKEN: long-lived subscription token (`claude setup-token`)
#  - VIPER_ANTHROPIC_BASE_URL + VIPER_ANTHROPIC_AUTH_TOKEN + VIPER_CLAUDE_MODEL: another model
#    behind an Anthropic-compatible API (Ollama's cloud, a vLLM server, a gateway); aliases,
#    background tasks and subagents use that model too
# The settings go through stdin into a 600 file; a line at the very top of .bashrc/.profile
# (before the "not interactive: return" guard) loads it for ssh commands, tmux and VS Code.
# Dedicated variables, so credentials exported locally are never copied to a server.
claude_env=''
if [[ -n "${VIPER_ANTHROPIC_BASE_URL:-}" ]]; then
  claude_env=$(printf 'export ANTHROPIC_BASE_URL=%q\nexport ANTHROPIC_AUTH_TOKEN=%q\nexport ANTHROPIC_API_KEY=""\nunset CLAUDE_CODE_OAUTH_TOKEN' \
    "$VIPER_ANTHROPIC_BASE_URL" "$VIPER_ANTHROPIC_AUTH_TOKEN")
  for v in ANTHROPIC_MODEL ANTHROPIC_DEFAULT_OPUS_MODEL ANTHROPIC_DEFAULT_SONNET_MODEL ANTHROPIC_DEFAULT_HAIKU_MODEL CLAUDE_CODE_SUBAGENT_MODEL; do
    claude_env+=$(printf '\nexport %s=%q' "$v" "$VIPER_CLAUDE_MODEL")
  done
  [[ -z "${VIPER_CLAUDE_MAX_CONTEXT_TOKENS:-}" ]] || claude_env+=$(printf '\nexport CLAUDE_CODE_MAX_CONTEXT_TOKENS=%q' "$VIPER_CLAUDE_MAX_CONTEXT_TOKENS")
  claude_login_msg="claude: using model $VIPER_CLAUDE_MODEL at $VIPER_ANTHROPIC_BASE_URL"
elif [[ -n "${VIPER_CLAUDE_TOKEN:-}" ]]; then
  claude_env=$(printf 'export CLAUDE_CODE_OAUTH_TOKEN=%q' "$VIPER_CLAUDE_TOKEN")
  claude_login_msg='claude: logged in with VIPER_CLAUDE_TOKEN'
fi
if [[ -n "$claude_env" ]]; then
  claude_login='umask 077 && mkdir -p ~/.config/viper && cat > ~/.config/viper/claude.env &&
    line="[ -f ~/.config/viper/claude.env ] && . ~/.config/viper/claude.env" &&
    for f in ~/.bashrc ~/.profile; do
      grep -qxF "$line" "$f" 2>/dev/null || { { echo "$line"; [ ! -f "$f" ] || cat "$f"; } > "$f.viper" && mv "$f.viper" "$f"; }
    done'
  ssh -F "$SSH_CFG" "$SSH_ALIAS" "$claude_login" <<<"$claude_env"
  echo "$claude_login_msg"
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
# Homebrew (official installer, as builder; skipped if already there). Not fatal: a failure
# is reported, and make check shows it.
if [[ "$VIPER_HOMEBREW" == 1 ]]; then
  echo 'Homebrew: installing (skipped if present) ...'
  if ! ssh -n -F "$SSH_CFG" "$SSH_ALIAS" "test -x $BREW || { mkdir -p ~/.config/viper && f=\$(mktemp) &&
      curl -fsSL -o \"\$f\" https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh &&
      NONINTERACTIVE=1 bash \"\$f\" </dev/null >~/.config/viper/homebrew-install.log 2>&1; rc=\$?; rm -f \"\$f\";
      [ \$rc = 0 ] || tail -n 15 ~/.config/viper/homebrew-install.log; exit \$rc; }"; then
    echo "WARNING: Homebrew installation failed (log on the server: ~/.config/viper/homebrew-install.log); rerun ./start.sh $MACHINE" >&2
  fi
fi
# Homebrew, in this order (not fatal): VIPER_BREW_TAPS ("owner/repo", or "owner/repo=URL" for
# a tap outside GitHub), VIPER_BREW_TRUST (brew trust: "owner/repo" = a tap, "owner/repo/name"
# = a formula, or "cask:…"/"command:…"), VIPER_BREW_PACKAGES ("name" or "owner/repo/name") and
# a Brewfile in the cloned repository (tap/brew lines, trusted: options; brew bundle).
# The Brewfile is Ruby from the repository and may make trust decisions: only with VIPER_BREWFILE=1.
brewfile=''; [[ "${VIPER_BREWFILE:-0}" != 1 || -z "${REPO_URL:-}" ]] || brewfile="$WORKDIR/Brewfile"
read -r -d '' -a brew_taps <<<"${VIPER_BREW_TAPS:-}" || true; read -r -d '' -a brew_trust <<<"${VIPER_BREW_TRUST:-}" || true
read -r -d '' -a brew_pkgs <<<"${VIPER_BREW_PACKAGES:-}" || true
if [[ "$VIPER_HOMEBREW" == 1 ]] && (( ${#brew_taps[@]} + ${#brew_trust[@]} + ${#brew_pkgs[@]} > 0 )) || [[ "$VIPER_HOMEBREW" == 1 && -n "$brewfile" ]]; then
  if ! ssh -F "$SSH_CFG" "$SSH_ALIAS" "bash -s -- $(printf '%q ' "$BREW" "$brewfile" "${#brew_taps[@]}" "${#brew_trust[@]}" "${brew_taps[@]}" "${brew_trust[@]}" "${brew_pkgs[@]}")" <<'BREWPKG'
set -uo pipefail
brew="$1"; brewfile="$2"; ntaps="$3"; ntrust="$4"; shift 4
taps=("${@:1:ntaps}"); trust=("${@:ntaps+1:ntrust}"); pkgs=("${@:ntaps+ntrust+1}")
[ -x "$brew" ] || { echo "brew: Homebrew missing, taps and formulae skipped"; exit 1; }
eval "$("$brew" shellenv)"
export HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_INSTALL_CLEANUP=1 NONINTERACTIVE=1
mkdir -p ~/.config/viper; log=~/.config/viper/brew-packages.log; : > "$log"
rc=0
for t in "${taps[@]}"; do
  name="${t%%=*}"; url=''; [ "$t" = "$name" ] || url="${t#*=}"
  if brew tap "$name" ${url:+"$url"} >>"$log" 2>&1 </dev/null; then echo "brew: tap $name"; else echo "brew: tap $name FAILED"; rc=1; fi
done
for t in "${trust[@]}"; do
  case "$t" in
    tap:*|formula:*|cask:*|command:*) kind="${t%%:*}"; name="${t#*:}" ;;
    */*/*) kind=formula; name="$t" ;;
    *) kind=tap; name="$t" ;;
  esac
  if brew trust "--$kind" "$name" >>"$log" 2>&1 </dev/null; then echo "brew: trusted $kind $name"; else echo "brew: trust $kind $name FAILED"; rc=1; fi
done
if (( ${#pkgs[@]} )); then
  if brew install "${pkgs[@]}" >>"$log" 2>&1 </dev/null; then echo "brew: installed ${pkgs[*]}"; else echo "brew: install ${pkgs[*]} FAILED"; rc=1; fi
fi
if [ -n "$brewfile" ] && [ -f "$brewfile" ]; then
  if brew bundle install --no-upgrade --file="$brewfile" >>"$log" 2>&1 </dev/null; then echo "brew: $brewfile applied"; else echo "brew: $brewfile FAILED"; rc=1; fi
fi
(( rc == 0 )) || tail -n 15 "$log"
exit "$rc"
BREWPKG
  then
    echo "WARNING: some Homebrew taps, trust entries or formulae failed (log on the server: ~/.config/viper/brew-packages.log); rerun ./start.sh $MACHINE" >&2
  fi
fi
# Interactive shells (make ssh, tmux, a plain ssh login) that start in the home directory
# change to the work directory: the line goes at the end of .bashrc, which non-interactive
# shells (ssh commands, the scripts) never reach because of Ubuntu's early return.
ssh -F "$SSH_CFG" "$SSH_ALIAS" "bash -s -- $(printf '%q ' "$WORKDIR" "$WORKSPACE_DIR" "$BREW" "$SERVER_ID")" <<'CDLINE'
set -eu
mkdir -p ~/.config/viper
printf '%s\n' "$1" > ~/.config/viper/workdir
# brew in every shell (also non-interactive ones, e.g. the agent's commands): top of the files
brew_line="[ -x $3 ] && eval \"\$($3 shellenv)\""
for f in ~/.bashrc ~/.profile; do
  grep -qxF -- "$brew_line" "$f" 2>/dev/null || { { echo "$brew_line"; [ ! -f "$f" ] || cat "$f"; } > "$f.viper" && mv "$f.viper" "$f"; }
done
grep -qxF -- "$2" ~/.config/viper/workspaces 2>/dev/null || printf '%s\n' "$2" >> ~/.config/viper/workspaces
line='if [ "$PWD" = "$HOME" ] && [ -f ~/.config/viper/workdir ]; then cd "$(cat ~/.config/viper/workdir)" 2>/dev/null || true; fi  # viper: start in the work directory'
grep -qxF -- "$line" ~/.bashrc 2>/dev/null || printf '%s\n' "$line" >> ~/.bashrc
# last step of a completed run: the server counts as set up (see REUSED), for any computer
printf '%s\n' "$4" > ~/.config/viper/ready
CDLINE
# from now on this server counts as set up (see REUSED)
echo "$SERVER_ID" > "$READY_STATE"
# What is installed, for the log (logs/<server>.log): the server and its own logs are gone
# once it is deleted. Packages of cloud-init.yaml, tools and Homebrew (taps, trust, the
# formulae installed on request). Informative only.
apt_pkgs=$(awk '/^packages:/ {f = 1; next} /^[a-z_]+:/ {f = 0} f && /^  - / {print $2}' cloud-init.yaml)
echo '== installed on the server =='
ssh -F "$SSH_CFG" "$SSH_ALIAS" "bash -s -- $(printf '%q ' "$BREW" $apt_pkgs)" <<'INVENTORY' || echo '(inventory not available)'
brew="$1"; shift
export LC_ALL=C PATH="$HOME/.local/bin:$PATH"
. /etc/os-release 2>/dev/null; echo "os: ${PRETTY_NAME:-?}, kernel $(uname -r)"
echo "apt: $(dpkg-query -W -f='${Package} ${Version}, ' "$@" 2>/dev/null | sed 's/, $//')"
echo "claude: $(claude --version 2>/dev/null | head -n1)"
echo "gh: $(gh --version 2>/dev/null | head -n1)"
echo "docker: $(docker --version 2>/dev/null)"
if [ -x "$brew" ]; then
  eval "$("$brew" shellenv)"; export HOMEBREW_NO_AUTO_UPDATE=1
  echo "homebrew: $(brew --version 2>/dev/null </dev/null | head -n1)"
  echo "brew taps: $(brew tap 2>/dev/null </dev/null | tr '\n' ' ')"
  echo "brew trusted: $(brew trust --json=v1 2>/dev/null </dev/null | jq -c '{taps, formulae, casks, commands} | with_entries(select(.value | length > 0))' 2>/dev/null)"
  leaves=$(brew leaves 2>/dev/null </dev/null)
  echo "brew formulae (installed on request): $( [ -z "$leaves" ] || brew list --versions $leaves 2>/dev/null </dev/null | tr '\n' ',' | sed 's/,$//; s/,/, /g')"
fi
INVENTORY
final_event "ready (work dir $WORKDIR)"
echo "Ready: ssh $SSH_ALIAS (work dir: $WORKDIR)"
# VS Code and its extensions must not inherit the API tokens from .env.local
if command -v code >/dev/null; then
  env -u HCLOUD_TOKEN -u CLOUDFLARE_API_TOKEN -u CLOUDFLARE_ZONE_ID -u VIPER_GH_TOKEN -u GH_TOKEN \
    -u VIPER_TRANSCRIPT -u VIPER_TRANSCRIPT_PID -u VIPER_ACTION \
    -u VIPER_CLAUDE_TOKEN -u CLAUDE_CODE_OAUTH_TOKEN -u VIPER_ANTHROPIC_AUTH_TOKEN code --remote "ssh-remote+$SSH_ALIAS" "$WORKDIR" || true
fi
