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
