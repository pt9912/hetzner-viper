set -uo pipefail
brew="$1"; brewfile="$2"; ntaps="$3"; ntrust="$4"; shift 4
taps=("${@:1:ntaps}"); trust=("${@:ntaps+1:ntrust}"); pkgs=("${@:ntaps+ntrust+1}")
[ -x "$brew" ] || { echo "brew: Homebrew missing, taps and formulae skipped"; exit 1; }
eval "$("$brew" shellenv)"
export HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_INSTALL_CLEANUP=1 NONINTERACTIVE=1
mkdir -p ~/.config/viper; log=~/.config/viper/brew-packages.log; : > "$log"
rc=0
for t in "${taps[@]}"; do
  name="${t%%=*}"; url=''; [ "$t" = "$name" ] || url="${t#*=}"
  if brew tap "$name" ${url:+"$url"} >>"$log" 2>&1 </dev/null; then echo "brew: tap $name"; else echo "brew: tap $name FAILED"; rc=1; fi
done
for t in "${trust[@]}"; do
  case "$t" in
    tap:*|formula:*|cask:*|command:*) kind="${t%%:*}"; name="${t#*:}" ;;
    */*/*) kind=formula; name="$t" ;;
    *) kind=tap; name="$t" ;;
  esac
  if brew trust "--$kind" "$name" >>"$log" 2>&1 </dev/null; then echo "brew: trusted $kind $name"; else echo "brew: trust $kind $name FAILED"; rc=1; fi
done
if (( ${#pkgs[@]} )); then
  if brew install "${pkgs[@]}" >>"$log" 2>&1 </dev/null; then echo "brew: installed ${pkgs[*]}"; else echo "brew: install ${pkgs[*]} FAILED"; rc=1; fi
fi
if [ -n "$brewfile" ] && [ -f "$brewfile" ]; then
  if brew bundle install --no-upgrade --file="$brewfile" >>"$log" 2>&1 </dev/null; then echo "brew: $brewfile applied"; else echo "brew: $brewfile FAILED"; rc=1; fi
fi
(( rc == 0 )) || tail -n 15 "$log"
exit "$rc"
