set -eu
mkdir -p ~/.config/viper
printf '%s\n' "$1" > ~/.config/viper/workdir
# brew in every shell (also non-interactive ones, e.g. the agent's commands): top of the files
brew_line="[ -x $3 ] && eval \"\$($3 shellenv)\""
for f in ~/.bashrc ~/.profile; do
  grep -qxF -- "$brew_line" "$f" 2>/dev/null || { { echo "$brew_line"; [ ! -f "$f" ] || cat "$f"; } > "$f.viper" && mv "$f.viper" "$f"; }
done
grep -qxF -- "$2" ~/.config/viper/workspaces 2>/dev/null || printf '%s\n' "$2" >> ~/.config/viper/workspaces
line='if [ "$PWD" = "$HOME" ] && [ -f ~/.config/viper/workdir ]; then cd "$(cat ~/.config/viper/workdir)" 2>/dev/null || true; fi  # viper: start in the work directory'
grep -qxF -- "$line" ~/.bashrc 2>/dev/null || printf '%s\n' "$line" >> ~/.bashrc
# last step of a completed run: the server counts as set up (see REUSED), for any computer
printf '%s\n' "$4" > ~/.config/viper/ready
