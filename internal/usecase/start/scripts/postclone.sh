set -u
dir="$1"; on_error="$2"
pending="$dir/.git/viper-post-clone-pending"
cmdfile=~/.config/viper/post-clone.sh   # the commands, written by viper (mode 600), not part of argv
log=~/.config/viper/post-clone.log
( umask 077; : > "$log" )   # the log may hold what the commands print; the commands keep the normal umask
cd "$dir" || { echo "post-clone: $dir missing"; exit 1; }
# login shell: brew and the other tools set up on the server are on PATH; stops at the first failing command
bash -le "$cmdfile" </dev/null 2>&1 | tee -a "$log"
# the status of the commands, not of tee (a log that cannot be written is no failure of the commands)
if [ "${PIPESTATUS[0]}" = 0 ]; then
  rm -f "$pending"; echo "post-clone: done"
else
  # ignore: this one attempt is the only one
  [ "$on_error" != ignore ] || rm -f "$pending"
  echo "post-clone: FAILED"; exit 1
fi
