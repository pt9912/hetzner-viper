set -uo pipefail
dir="$1"; cmds="$2"; on_error="$3"
umask 077   # the log may hold what the commands print
mkdir -p ~/.config/viper; log=~/.config/viper/post-clone.log
pending="$dir/.git/viper-post-clone-pending"
[ -e "$pending" ] || exit 0   # only right after the clone
cd "$dir" || { echo "post-clone: $dir missing"; exit 1; }
# login shell: brew and the other tools set up on the server are on PATH; stops at the first failing command
if bash -lec "$cmds" </dev/null 2>&1 | tee "$log"; then
  rm -f "$pending"; echo "post-clone: done"
else
  # ignore: this one attempt is the only one
  [ "$on_error" != ignore ] || rm -f "$pending"
  echo "post-clone: FAILED"; exit 1
fi
