#!/usr/bin/env bash
set -euo pipefail
MACHINE="${1:?Usage: ./stop.sh <machine> [--force]}"; export MACHINE
source "$(dirname "$0")/common.sh"
start_transcript down

# Remove the local SSH config of this machine.
cleanup_local() {
  ssh_include remove || ssh_include_warning
  rm -f "$SSH_CFG" "$KNOWN_PIN" "$IP_STATE" "$ID_STATE" "$WORKSPACE_STATE" "$READY_STATE" "$REPO_STATE"
}

if ! hcloud_has server "$SERVER_NAME"; then
  # Possibly the wrong Hetzner project (token): touch nothing without confirmation, and
  # delete DNS only if it still points to the IP this machine had.
  echo "Server $SERVER_NAME not found in this Hetzner project."
  old_ip=$(cat "$IP_STATE" 2>/dev/null || true)
  left=0
  for f in "$SSH_CFG" "$KNOWN_PIN" "$IP_STATE" "$ID_STATE" "$WORKSPACE_STATE" "$READY_STATE" "$REPO_STATE"; do
    [[ ! -e "$f" ]] || left=1
  done
  if (( ! left )); then
    echo 'Nothing left to clean up.'
    final_event "nothing to clean up (server not found, no local files)"
    exit 0
  fi
  echo "Leftovers: local SSH config and pinned host key${old_ip:+, DNS record $DNS_HOST -> $old_ip}."
  read -r -p 'Type CLEANUP to remove them: ' answer
  [[ "$answer" == CLEANUP ]] || { echo 'Cancelled'; final_event "cancelled (server not found, no CLEANUP)"; exit 1; }
  if [[ -n "$old_ip" ]]; then
    ./dns.sh delete "$old_ip" || echo 'WARNING: DNS removal failed; remove the stale record manually.' >&2
  else
    echo "No recorded IP: DNS record $DNS_HOST left untouched."
  fi
  cleanup_local
  final_event "cleaned up (server not found in the project)"
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
  elif [[ "$(ssh_failure_kind "$err")" == auth ]]; then
    echo "The server answers, but rejects the login key $SSH_KEY: $err" >&2
    echo "It was probably created with another login key (another computer or SSH_KEY). Use that one to check and push its work." >&2
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
unsaved=0
# workspaces: those passed in (recorded on this computer) plus those the server recorded
# itself (also started from other computers)
declare -A seen_ws; ws=()
while IFS= read -r w; do
  [[ -n "$w" && -z "${seen_ws[$w]:-}" ]] || continue
  seen_ws[$w]=1; ws+=("$w")
done < <(printf '%s\n' "$@"; cat ~/.config/viper/workspaces 2>/dev/null)
declare -A seen

# One working tree: uncommitted changes, detached commits, ignored files.
check_tree() {
  local t="$1" dirty hidden detached ignored
  echo "== $t =="
  # fixed options: repo or user config (status.showUntrackedFiles=no, submodule ignore) must
  # not hide anything here
  if ! dirty=$(git -C "$t" -c status.showUntrackedFiles=all status --porcelain --untracked-files=all --ignore-submodules=none 2>&1); then
    echo "ERROR: cannot read git status: $dirty"; unsaved=1; return
  fi
  [[ -z "$dirty" ]] || { echo "$dirty"; unsaved=1; }
  # files marked assume-unchanged (lowercase tag) or skip-worktree (S) are hidden from status
  hidden=$(git -C "$t" ls-files -v 2>/dev/null | grep -E '^([a-z]|S) ' || true)
  [[ -z "$hidden" ]] || { printf 'Hidden from git status (assume-unchanged / skip-worktree):\n%s\n' "$hidden"; unsaved=1; }
  if ! git -C "$t" symbolic-ref -q HEAD >/dev/null; then
    detached=$(git -C "$t" log --oneline HEAD --not --remotes --branches --tags)
    [[ -z "$detached" ]] || { printf 'Detached commits not on any branch or remote:\n%s\n' "$detached"; unsaved=1; }
  fi
  ignored=$(git -C "$t" -c status.showUntrackedFiles=normal status --porcelain --ignored --ignore-submodules=none | grep '^!!' || true)
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
  # Tags count only if created here: not among those that came with the clone (recorded by
  # start.sh) and not on any remote (asked with a time limit; unknown if offline).
  local_tags=$(git -C "$repo" for-each-ref --format='%(refname)' refs/tags)
  remote_tags=$(for r in $(git -C "$repo" remote); do timeout 20 git -C "$repo" ls-remote --tags "$r" 2>/dev/null </dev/null | awk '{print $2}' | sed 's/\^{}$//'; done)
  new_tags=$(comm -23 <(sort -u <<<"$local_tags") <(sort -u <(cat "$common/viper-clone-tags" 2>/dev/null; printf '%s\n' "$remote_tags")) | grep . || true)
  # shellcheck disable=SC2086  # ref names have no spaces
  unpushed=$(git -C "$repo" log --oneline --branches $new_tags --not --remotes)
  [[ -z "$unpushed" ]] || { printf 'Commits not on any remote (branches and tags):\n%s\n' "$unpushed"; unsaved=1; }
  # a remote that is a path on this server is deleted together with it
  while read -r name url; do
    case "$url" in
      /*|./*|../*|file://*) printf 'Remote %s is on this server (%s): pushed commits are lost too\n' "$name" "$url"; unsaved=1 ;;
    esac
  done < <(git -C "$repo" remote -v | awk '$3 == "(push)" {print $1, $2}')
  stashes=$(git -C "$repo" stash list)
  [[ -z "$stashes" ]] || { echo "$stashes"; unsaved=1; }
}

for MP in "${ws[@]}"; do
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
done < <(find "${ws[@]}" "$HOME" ${VIPER_TMP_DIRS:-/tmp /var/tmp} -xdev \
           \( -name node_modules -o -path "$HOME/.cache" -o -path "$HOME/.local" -o -path "$HOME/.npm" \
              -o -path "$HOME/.vscode-server" \) -prune -o -name .git -prune -print0 2>/dev/null)
echo '== tmux sessions =='
tmux ls 2>/dev/null || true
(( unsaved == 0 )) || echo 'UNSAVED WORK: uncommitted, unpushed, stashed or unversioned data exists and will be lost.'
# without this line the check did not run to the end (e.g. a shell startup file exited)
echo '== end of check =='
REMOTE
) || { echo 'Git check on the server failed. NOT deleting server (use --force to skip checks).' >&2; exit 1; }
# report, warning and prompts on one stream (stderr, like read -p), so they stay in order
echo "$report" >&2
grep -qx '== end of check ==' <<<"$report" || { echo 'The git check on the server did not complete. NOT deleting server (use --force to skip checks).' >&2; final_event "refused (git check incomplete)"; exit 1; }
if grep -q '^UNSAVED WORK' <<<"$report"; then
  read -r -p 'Unsaved work will be lost. Type DISCARD to continue: ' discard
  [[ "$discard" == DISCARD ]] || { echo 'Cancelled: commit and push first'; final_event "cancelled (unsaved work)"; exit 1; }
fi
}
printf '\nServer %s (%s) and ALL its data (including %s) will be DESTROYED.\n' "$SERVER_NAME" "$IP" "${WSS[*]}" >&2
read -r -p 'Type DELETE to continue: ' confirm
[[ "$confirm" == DELETE ]] || { echo 'Cancelled'; final_event "cancelled (no DELETE)"; exit 1; }
hcloud server delete "$SERVER_NAME"
log_event "server deleted id=$SERVER_ID ip=$IP$( (( SKIP )) && echo ' (--force, no git checks)')"
./dns.sh delete "$IP" || echo 'WARNING: DNS removal failed; remove the stale record manually.' >&2
cleanup_local
final_event "deleted"
echo "Server $SERVER_NAME deleted."
