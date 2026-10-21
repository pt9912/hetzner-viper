#!/usr/bin/env bash
set -euo pipefail
MACHINE="${1:?Usage: ./stop.sh <machine> [--force]}"; export MACHINE
source "$(dirname "$0")/common.sh"

# Remove the local SSH config of this machine.
cleanup_local() {
  ssh_include remove || ssh_include_warning
  rm -f "$SSH_CFG" "$KNOWN_PIN" "$IP_STATE" "$ID_STATE" "$WORKSPACE_STATE"
}

if ! hcloud_has server "$SERVER_NAME"; then
  # Possibly the wrong Hetzner project (token): touch nothing without confirmation, and
  # delete DNS only if it still points to the IP this machine had.
  echo "Server $SERVER_NAME not found in this Hetzner project."
  old_ip=$(cat "$IP_STATE" 2>/dev/null || true)
  echo "Leftovers: local SSH config and pinned host key${old_ip:+, DNS record $DNS_HOST -> $old_ip}."
  read -r -p 'Type CLEANUP to remove them: ' answer
  [[ "$answer" == CLEANUP ]] || { echo 'Cancelled'; exit 1; }
  if [[ -n "$old_ip" ]]; then
    ./dns.sh delete "$old_ip" || echo 'WARNING: DNS removal failed; remove the stale record manually.' >&2
  else
    echo "No recorded IP: DNS record $DNS_HOST left untouched."
  fi
  cleanup_local
  exit 0
fi
IP=$(hcloud server ip "$SERVER_NAME")
SERVER_ID=$(server_id)
forget_stale_pin "$SERVER_ID"
write_ssh_config "$IP"
# Check the directory the server was started with, even if WORKSPACE_DIR changed since.
# every workspace the server was started with (start.sh records each), else the configured one
mapfile -t WSS < <(grep -v '^$' "$WORKSPACE_STATE" 2>/dev/null || true)
(( ${#WSS[@]} > 0 )) || WSS=("$WORKSPACE_DIR")
FORCE=0; [[ "${2:-}" == --force ]] && FORCE=1
# -n: ssh must not swallow the answers that follow on stdin. The git checks run whenever
# the server is reachable and trusted, with or without --force.
SKIP=0
if ! err=$(ssh -n -F "$SSH_CFG" -o BatchMode=yes -o ConnectTimeout=10 "$SSH_ALIAS" true 2>&1 >/dev/null); then
  if [[ "$(ssh_failure_kind "$err")" == hostkey ]]; then
    echo "The server answers, but its host key is not trusted: $err" >&2
    echo "It may have been recreated from another computer (stale pin) or its certificate expired without a pin." >&2
    echo 'Unsaved work on it cannot be checked. Only if you are sure it holds nothing of value: ./stop.sh <machine> --force' >&2
  else
    echo 'SSH unreachable. Use ./stop.sh <machine> --force to delete without checks.' >&2
  fi
  (( FORCE )) || exit 1
  echo 'WARNING: --force skips the git checks; everything on the server is lost.' >&2
  SKIP=1
elif [[ ! -s "$KNOWN_PIN" ]]; then
  pin_from_server "$SERVER_ID"
  write_ssh_config "$IP"
fi
(( SKIP )) || {
echo 'Checking git repositories, loose files and running sessions ...'
# No `set -e` remotely: a git error must be reported as unsaved work, not abort the check.
report=$(ssh -F "$SSH_CFG" "$SSH_ALIAS" "bash -s -- $(printf '%q ' "${WSS[@]}")" <<'REMOTE'
set -uo pipefail
unsaved=0   # "$@": the workspaces
declare -A seen

# One working tree: uncommitted changes, detached commits, ignored files.
check_tree() {
  local t="$1" dirty detached ignored
  echo "== $t =="
  if ! dirty=$(git -C "$t" status --short 2>&1); then
    echo "ERROR: cannot read git status: $dirty"; unsaved=1; return
  fi
  [[ -z "$dirty" ]] || { echo "$dirty"; unsaved=1; }
  if ! git -C "$t" symbolic-ref -q HEAD >/dev/null; then
    detached=$(git -C "$t" log --oneline HEAD --not --remotes --branches)
    [[ -z "$detached" ]] || { printf 'Detached commits not on any branch or remote:\n%s\n' "$detached"; unsaved=1; }
  fi
  ignored=$(git -C "$t" status --short --ignored | grep '^!!' || true)
  [[ -z "$ignored" ]] || printf 'Ignored by git (also lost):\n%s\n' "$ignored"
}

# One repository (found via its .git directory or a worktree's .git file).
check_repo() {
  local repo="$1" common tree unpushed stashes
  if ! common=$(git -C "$repo" rev-parse --path-format=absolute --git-common-dir 2>&1); then
    echo "== $repo =="; echo "ERROR: cannot read git repository: $common"; unsaved=1; return
  fi
  [[ -z "${seen[$common]:-}" ]] || return 0
  seen[$common]=1
  # every working tree of the repository, also linked worktrees elsewhere
  while IFS= read -r tree; do
    check_tree "$tree"
  done < <(git -C "$repo" worktree list --porcelain | sed -n 's/^worktree //p')
  # commits on any local branch that are not on any remote (also branches without upstream)
  unpushed=$(git -C "$repo" log --oneline --branches --not --remotes)
  [[ -z "$unpushed" ]] || { printf 'Commits not on any remote:\n%s\n' "$unpushed"; unsaved=1; }
  stashes=$(git -C "$repo" stash list)
  [[ -z "$stashes" ]] || { echo "$stashes"; unsaved=1; }
}

for MP in "$@"; do
  if [[ ! -d "$MP" ]]; then
    echo "ERROR: workspace $MP does not exist"; unsaved=1
  else
    # Anything at the top of the workspace that is not a git repository is unprotected.
    loose=$(find "$MP" -mindepth 1 -maxdepth 1 ! -exec test -e '{}/.git' ';' -print)
    [[ -z "$loose" ]] || { printf 'Not in any git repository (lost):\n%s\n' "$loose"; unsaved=1; }
  fi
done
# Repositories in the workspace, in the home directory and in the temp directories
# (the agent may clone or init repos anywhere); caches and dependencies are skipped.
# VIPER_TMP_DIRS only exists for the offline tests; ssh does not forward it.
while IFS= read -r -d '' gitpath; do
  check_repo "${gitpath%/.git}"
done < <(find "$@" "$HOME" ${VIPER_TMP_DIRS:-/tmp /var/tmp} -xdev \
           \( -name node_modules -o -path "$HOME/.cache" -o -path "$HOME/.local" -o -path "$HOME/.npm" \
              -o -path "$HOME/.vscode-server" \) -prune -o -name .git -prune -print0 2>/dev/null)
echo '== tmux sessions =='
tmux ls 2>/dev/null || true
(( unsaved == 0 )) || echo 'UNSAVED WORK: uncommitted, unpushed, stashed or unversioned data exists and will be lost.'
REMOTE
) || { echo 'Git check on the server failed. NOT deleting server (use --force to skip checks).' >&2; exit 1; }
echo "$report"
if grep -q '^UNSAVED WORK' <<<"$report"; then
  read -r -p 'Unsaved work will be lost. Type DISCARD to continue: ' discard
  [[ "$discard" == DISCARD ]] || { echo 'Cancelled: commit and push first'; exit 1; }
fi
}
printf '\nServer %s (%s) and ALL its data (including %s) will be DESTROYED.\n' "$SERVER_NAME" "$IP" "${WSS[*]}"
read -r -p 'Type DELETE to continue: ' confirm
[[ "$confirm" == DELETE ]] || { echo 'Cancelled'; exit 1; }
hcloud server delete "$SERVER_NAME"
./dns.sh delete "$IP" || echo 'WARNING: DNS removal failed; remove the stale record manually.' >&2
cleanup_local
echo "Server $SERVER_NAME deleted."
