set -u
dir="$1"; on_error="$2"; brew="$3"
pending="$dir/.git/viper-post-clone-pending"
cmdfile=~/.config/viper/post-clone.sh   # the commands, written by viper (mode 600), not part of argv
log=~/.config/viper/post-clone.log
( umask 077; : > "$log" )   # the log may hold what the commands print; the commands keep the normal umask
cd "$dir" || { echo "post-clone: $dir missing"; exit 1; }
# Login shell, so the tools set up on the server are on PATH. brew is added inside it, after the profile:
# /etc/profile resets PATH, and the lines in .bashrc/.profile only come with the last step (cdline.sh), after this.
# The commands run in their own bash that stops at the first failing command.
bash -lc '[ ! -x "$1" ] || eval "$("$1" shellenv)"; export HOMEBREW_NO_AUTO_UPDATE=1; exec bash -e "$2"' _ "$brew" "$cmdfile" </dev/null 2>&1 | tee -a "$log"
# the status of the commands, not of tee (a log that cannot be written is no failure of the commands)
if [ "${PIPESTATUS[0]}" = 0 ]; then
  rm -f "$pending"; echo "post-clone: done"
else
  # ignore: this one attempt is the only one
  [ "$on_error" != ignore ] || rm -f "$pending"
  echo "post-clone: FAILED"; exit 1
fi
