set -u
name="$1"; email="$2"; host="$3"; use_gh="$4"
[ -z "$name" ] || git config --global user.name "$name"
[ -z "$email" ] || git config --global user.email "$email"
if [ -n "$use_gh" ] && { [ -z "$(git config --global user.name)" ] || [ -z "$(git config --global user.email)" ]; }; then
  info=$(gh api --hostname "$host" user -q '[.login, (.id | tostring), (.name // "")] | @tsv' 2>/dev/null </dev/null) || info=''
  if [ -n "$info" ]; then
    IFS=$'\t' read -r login id gname <<<"$info"
    [ -n "$(git config --global user.name)" ] || git config --global user.name "${gname:-$login}"
    [ "$host" != github.com ] || [ -n "$(git config --global user.email)" ] || git config --global user.email "$id+$login@users.noreply.github.com"
  fi
fi
n=$(git config --global user.name); e=$(git config --global user.email)
if [ -n "$n" ] && [ -n "$e" ]; then
  echo "git: commits as $n <$e>"
else
  echo "WARNING: git user.name/user.email are not set on the server, commits will fail (VIPER_GIT_USER_NAME, VIPER_GIT_USER_EMAIL)" >&2
fi
