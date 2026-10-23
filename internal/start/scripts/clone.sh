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
  # tags that came with the clone: stop.sh only counts tags created later as unsaved work
  git -C "$dir" for-each-ref --format='%(refname)' refs/tags > "$dir/.git/viper-clone-tags"
fi
[[ -z "$use_key" ]] || git -C "$dir" config core.sshCommand "$GIT_SSH_COMMAND"
