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
(( ${#missing[@]} == 0 )) || { echo "Missing settings: ${missing[*]} (in .env.local or machines/$MACHINE.env.local; the three tokens may also be exported in the shell)" >&2; exit 1; }
[[ "$DNS_DOMAIN" =~ ^([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,63}$ ]] || { echo "DNS_DOMAIN must be a domain like example.com (lowercase, no scheme, no trailing dot): $DNS_DOMAIN" >&2; exit 1; }
for cmd in hcloud jq curl ssh ssh-keygen python3; do command -v "$cmd" >/dev/null || { echo "Missing: $cmd" >&2; exit 1; }; done
SSH_KEY="${SSH_KEY:-$HOME/.ssh/hetzner-build}"
SSH_KEY="${SSH_KEY/#\~/$HOME}"
SERVER_TYPE="${SERVER_TYPE:-cx53}"
SERVER_LOCATION="${SERVER_LOCATION:-fsn1}"
SERVER_IMAGE="${SERVER_IMAGE:-ubuntu-26.04}"
VIPER_HOMEBREW="${VIPER_HOMEBREW:-1}"
# where Homebrew's installer puts brew on Linux (VIPER_BREW only exists for the offline tests)
BREW="${VIPER_BREW:-/home/linuxbrew/.linuxbrew/bin/brew}"
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
# Server ID of the last start.sh run that completed: only such a server counts as set up.
READY_STATE="$STATE/ready-$SERVER_NAME"
# Repository the server was started with (e.g. given once as REPO=...), for later runs.
REPO_STATE="$STATE/repo-$SERVER_NAME"

# resolve_repo_url <arg>: the repository of this machine: the argument, else REPO_URL from the
# config files, else the one this server was started with.
resolve_repo_url() {
  if [[ -n "${1:-}" ]]; then REPO_URL="$1"
  elif [[ -z "${REPO_URL:-}" && -s "$REPO_STATE" ]]; then REPO_URL=$(cat "$REPO_STATE"); REPO_REMEMBERED=1
  fi
}

# Logs (no tokens: the scripts never print them):
#  - events.log: one line per event (time, server, action, what happened)
#  - logs/<server>.log: the full output of every run of start/stop/check/preflight
EVENTS_LOG="$STATE/events.log"
LOG_DIR="$STATE/logs"
mkdir -p "$LOG_DIR"; chmod 700 "$LOG_DIR"
VIPER_ACTION="${VIPER_ACTION:-}"; FINAL_EVENT=0
EXIT_CMDS=()

# local time with its offset to UTC, so entries stay unambiguous (e.g. 2026-10-21T15:12:03+02:00)
now() { date +%Y-%m-%dT%H:%M:%S%:z; }

log_event() {
  ( umask 077; printf '%s %-24s %-9s %s\n' "$(now)" "$SERVER_NAME" "$VIPER_ACTION" "$*" >> "$EVENTS_LOG" )
}
# final_event <text>: the outcome of this run; without one, a failing exit is logged as FAILED
final_event() { log_event "$*"; FINAL_EVENT=1; }

# run_exit: cleanups registered in EXIT_CMDS, the FAILED event, then flush the transcript
run_exit() {
  local rc=$?
  for c in "${EXIT_CMDS[@]}"; do eval "$c" || true; done
  if [[ -n "${TRANSCRIPT_OWNER:-}" ]]; then
    (( FINAL_EVENT || rc == 0 )) || log_event "FAILED (exit $rc; see logs/$SERVER_NAME.log)"
    exec 1>&- 2>&-
    wait "$TEE_OUT" "$TEE_ERR" 2>/dev/null || true
  fi
  exit "$rc"
}
trap run_exit EXIT
# Ctrl-C, a closed terminal or kill: in the EXIT trap $? would be 0, so record it here
on_signal() {
  [[ -z "${TRANSCRIPT_OWNER:-}" ]] || { log_event "interrupted"; FINAL_EVENT=1; }
  exit 130
}
trap on_signal INT TERM HUP

# redact_url <url>: hide credentials in a URL (https://user:token@host -> https://***@host)
redact_url() { sed -E 's#^([a-z+]+://)[^/@]+@#\1***@#' <<<"$1"; }
# redact_taps <entries...>: the same for "owner/repo=URL" tap entries, as one line
redact_taps() {
  local t out=()
  for t in "$@"; do if [[ "$t" == *=* ]]; then out+=("${t%%=*}=$(redact_url "${t#*=}")"); else out+=("$t"); fi; done
  echo "${out[*]}"
}

# start_transcript <action>: copy all output of this run into logs/<server>.log. Scripts
# started from it (preflight.sh, dns.sh) write into the same transcript and no own events.
start_transcript() {
  VIPER_ACTION="$1"
  # nested only while the run that opened the transcript is still alive (not, say, in a
  # VS Code terminal that inherited the variables from an earlier start.sh)
  if [[ -n "${VIPER_TRANSCRIPT:-}" && -n "${VIPER_TRANSCRIPT_PID:-}" ]] && kill -0 "$VIPER_TRANSCRIPT_PID" 2>/dev/null; then
    return 0
  fi
  export VIPER_TRANSCRIPT="$LOG_DIR/$SERVER_NAME.log" VIPER_ACTION VIPER_TRANSCRIPT_PID=$$; TRANSCRIPT_OWNER=1
  ( umask 077; printf '\n===== %s %s %s\n' "$(now)" "$1" "$SERVER_NAME" >> "$VIPER_TRANSCRIPT" )
  exec > >(tee -a "$VIPER_TRANSCRIPT"); TEE_OUT=$!
  exec 2> >(tee -a "$VIPER_TRANSCRIPT" >&2); TEE_ERR=$!
}

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
    # the server is root-equivalent for the agent: never hand it the local ssh-agent or X11
    ForwardAgent no
    ForwardX11 no
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
# Returns 1 (without aborting the caller) if the file cannot be edited: the scripts
# themselves use -F, the Include only serves plain `ssh <alias>` and VS Code.
ssh_include() {
  local cfg line="Include $SSH_CFG" tmp rc=0
  cfg=$(readlink -f "$HOME/.ssh/config") || return 1
  { [[ -e "$cfg" ]] || touch "$cfg"; } 2>/dev/null && [[ -w "$cfg" ]] || return 1
  exec 9>"$STATE/ssh-config.lock"
  if command -v flock >/dev/null; then flock 9; fi
  tmp=$(mktemp)
  if [[ "$1" == add ]]; then
    grep -Fqx "$line" "$cfg" || { { echo "$line"; cat "$cfg"; } > "$tmp" && cat "$tmp" > "$cfg"; } || rc=1
  else
    { grep -Fvx "$line" "$cfg" > "$tmp" || true; } && cat "$tmp" > "$cfg" || rc=1
  fi
  rm -f "$tmp"
  chmod 600 "$cfg" 2>/dev/null || true
  exec 9>&-
  return "$rc"
}

ssh_include_warning() {
  echo "WARNING: could not edit ~/.ssh/config ($(readlink -f "$HOME/.ssh/config" 2>/dev/null)); add or remove the line 'Include $SSH_CFG' yourself." >&2
}

# ssh_failure_kind <stderr>: "hostkey" if ssh rejected the server's identity, "auth" if the
# server rejected our key, else "network".
ssh_failure_kind() {
  if grep -qiE 'host key verification failed|remote host identification has changed|host key for .* has changed|no .* host key is known|certificate invalid' <<<"$1"; then
    echo hostkey
  elif grep -qiE 'permission denied' <<<"$1"; then
    echo auth
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
