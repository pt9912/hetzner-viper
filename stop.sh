#!/usr/bin/env bash
set -euo pipefail
MACHINE="${1:?Usage: ./stop.sh <machine> [--force]}"; export MACHINE
source "$(dirname "$0")/common.sh"

# Remove everything this machine left on the local side and in DNS.
cleanup() {
  ./dns.sh delete "$@" || echo 'WARNING: DNS removal failed; remove the stale record manually.' >&2
  if [[ -f "$HOME/.ssh/config" ]] && grep -Fqx "Include $SSH_CFG" "$HOME/.ssh/config"; then
    grep -Fvx "Include $SSH_CFG" "$HOME/.ssh/config" > "$HOME/.ssh/config.new" || true
    mv "$HOME/.ssh/config.new" "$HOME/.ssh/config"
    chmod 600 "$HOME/.ssh/config"
  fi
  rm -f "$SSH_CFG" "$KNOWN_PIN" "$STATE/ip-$MACHINE" "$WORKSPACE_STATE"
}

if ! hcloud_has server "$SERVER_NAME"; then
  echo 'Server already absent; cleaning up DNS record and local SSH config.'
  cleanup
  exit 0
fi
IP=$(hcloud server ip "$SERVER_NAME")
write_ssh_config "$IP"
# Check the directory the server was started with, even if WORKSPACE_DIR changed since.
WS=$(cat "$WORKSPACE_STATE" 2>/dev/null || echo "$WORKSPACE_DIR")
FORCE=0; [[ "${2:-}" == --force ]] && FORCE=1
if (( FORCE )) || ! ssh -F "$SSH_CFG" -o BatchMode=yes -o ConnectTimeout=10 "$SSH_ALIAS" true 2>/dev/null; then
  (( FORCE )) || { echo 'SSH unreachable. Use ./stop.sh <machine> --force to delete without checks.' >&2; exit 1; }
  echo 'WARNING: --force skips the git checks; everything on the server is lost.' >&2
  SKIP=1
else SKIP=0
fi
(( SKIP )) || {
echo 'Checking git repositories and running sessions ...'
# No `set -e` remotely: a git error must be reported as unsaved work, not abort the check.
report=$(ssh -F "$SSH_CFG" "$SSH_ALIAS" "bash -s -- $(printf %q "$WS")" <<'REMOTE'
set -uo pipefail
MP="$1"; unsaved=0
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

# .git directories (repositories) and .git files (linked worktrees)
while IFS= read -r -d '' gitpath; do
  repo="${gitpath%/.git}"
  if ! common=$(git -C "$repo" rev-parse --path-format=absolute --git-common-dir 2>&1); then
    echo "== $repo =="; echo "ERROR: cannot read git repository: $common"; unsaved=1; continue
  fi
  [[ -z "${seen[$common]:-}" ]] || continue
  seen[$common]=1
  # every working tree of the repository, also linked worktrees outside the workspace
  while IFS= read -r tree; do
    check_tree "$tree"
  done < <(git -C "$repo" worktree list --porcelain | sed -n 's/^worktree //p')
  # commits on any local branch that are not on any remote (also branches without upstream)
  unpushed=$(git -C "$repo" log --oneline --branches --not --remotes)
  [[ -z "$unpushed" ]] || { printf 'Commits not on any remote:\n%s\n' "$unpushed"; unsaved=1; }
  stashes=$(git -C "$repo" stash list)
  [[ -z "$stashes" ]] || { echo "$stashes"; unsaved=1; }
done < <(find "$MP" -name .git -prune -print0)
echo '== tmux sessions =='
tmux ls 2>/dev/null || true
(( unsaved == 0 )) || echo 'UNSAVED WORK: uncommitted, unpushed or stashed changes exist and will be lost.'
REMOTE
) || { echo 'Git check on the server failed. NOT deleting server (use --force to skip checks).' >&2; exit 1; }
echo "$report"
if grep -q '^UNSAVED WORK' <<<"$report"; then
  read -r -p 'Unsaved work will be lost. Type DISCARD to continue: ' discard
  [[ "$discard" == DISCARD ]] || { echo 'Cancelled: commit and push first'; exit 1; }
fi
}
printf '\nServer %s (%s) and ALL its data (including %s) will be DESTROYED.\n' "$SERVER_NAME" "$IP" "$WS"
read -r -p 'Type DELETE to continue: ' confirm
[[ "$confirm" == DELETE ]] || { echo 'Cancelled'; exit 1; }
hcloud server delete "$SERVER_NAME"
cleanup "$IP"
echo "Server $SERVER_NAME deleted."
