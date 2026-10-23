set -uo pipefail
dir="$1"; ask_claude="$2"; brew="$3"; taps_in="$4"; pkgs_in="$5"; brewfile="$6"; trust_in="$7"; cs="$8"; cs_port="$9"
export LC_ALL=C PATH="$HOME/.local/bin:$PATH"
echo "cloudinit=$(cloud-init status 2>/dev/null | sed -n 's/^status: //p')"
# errors of the first boot stay recorded here, also once cloud-init is disabled
# (VIPER_CLOUD_RESULT only exists for the offline tests)
result="${VIPER_CLOUD_RESULT:-/var/lib/cloud/data/result.json}"
echo "cloudinit_errors=$(jq -r '.v1.errors | length' "$result" 2>/dev/null)"
echo "cloudinit_error=$(jq -r '.v1.errors[0] // empty' "$result" 2>/dev/null | head -c 200)"
echo "claude_version=$(claude --version 2>/dev/null | head -n1)"
# the same loader ssh commands, tmux and VS Code terminals use
echo "claude_auth=$(bash -c '. ~/.bashrc >/dev/null 2>&1
  if [ -n "${ANTHROPIC_BASE_URL:-}" ]; then echo "gateway ${ANTHROPIC_MODEL:-} ${ANTHROPIC_BASE_URL}"
  elif [ -n "${CLAUDE_CODE_OAUTH_TOKEN:-}" ]; then echo token; else echo none; fi')"
if [ -n "$ask_claude" ]; then
  # </dev/null: claude -p reads stdin as extra prompt and would swallow the rest of this script
  answer=$(cd "$dir" 2>/dev/null || cd; bash -c '. ~/.bashrc >/dev/null 2>&1; timeout 120 claude -p "Reply with exactly: OK"' </dev/null 2>&1 | tail -n1)
  echo "claude_answer=$answer"
fi
echo "gh_version=$(gh --version 2>/dev/null | head -n1)"
if gh auth status >/dev/null 2>&1; then echo gh_auth=yes; else echo gh_auth=no; fi
echo "git_name=$(git config --global user.name 2>/dev/null)"
echo "git_email=$(git config --global user.email 2>/dev/null)"
if docker info >/dev/null 2>&1; then echo docker=ok; else echo docker=no; fi
echo "swap_mb=$(swapon --show=SIZE --bytes --noheadings 2>/dev/null | awk '{s += $1} END {printf "%d", s / 1048576}')"
if [ "$cs" = 1 ]; then
  echo "code_server_version=$(code-server --version 2>/dev/null | head -n1)"
  echo "code_server_active=$(systemctl is-active code-server@builder 2>/dev/null)"
  if curl -fsS -m 5 -o /dev/null "http://127.0.0.1:$cs_port/healthz" 2>/dev/null; then echo code_server_http=yes; else echo code_server_http=no; fi
fi
echo "brew_version=$("$brew" --version 2>/dev/null | head -n1)"
if bash -c '. ~/.bashrc >/dev/null 2>&1; command -v brew' >/dev/null 2>&1; then echo brew_path=yes; else echo brew_path=no; fi
if [ -x "$brew" ]; then
  eval "$("$brew" shellenv)"; export HOMEBREW_NO_AUTO_UPDATE=1
  missing=''
  for t in $taps_in; do n="${t%%=*}"; brew tap 2>/dev/null </dev/null | grep -qixF -- "$n" || missing="$missing tap:$n"; done
  for p in $pkgs_in; do brew list --formula --versions "${p##*/}" >/dev/null 2>&1 </dev/null || missing="$missing $p"; done
  if [ -n "$trust_in" ]; then
    trusted=$(brew trust --json=v1 2>/dev/null </dev/null)
    for t in $trust_in; do
      case "$t" in
        tap:*|formula:*|cask:*|command:*) kind="${t%%:*}"; name="${t#*:}" ;;
        */*/*) kind=formula; name="$t" ;;
        *) kind=tap; name="$t" ;;
      esac
      key=$(case "$kind" in tap) echo taps ;; formula) echo formulae ;; cask) echo casks ;; command) echo commands ;; esac)
      jq -e --arg k "$key" --arg n "$name" '(.[$k] // []) | map(ascii_downcase) | index($n | ascii_downcase)' <<<"$trusted" >/dev/null 2>&1 ||
        missing="$missing trust:$name"
      # brew trust accepts any name: a trusted tap that is not tapped is likely a typo
      if [ "$kind" = tap ] && ! brew tap 2>/dev/null </dev/null | grep -qixF -- "$name"; then missing="$missing untapped:$name"; fi
    done
  fi
  echo "brew_missing=${missing# }"
  if [ -n "$brewfile" ] && [ -f "$brewfile" ]; then
    if brew bundle check --no-upgrade --file="$brewfile" >/dev/null 2>&1 </dev/null; then echo brewfile=ok; else echo brewfile=missing; fi
  fi
fi
if [ -d "$dir/.git" ]; then
  echo "repo=$(git -C "$dir" log --oneline -1 2>/dev/null || echo '(no commits yet)')"
  echo "repo_changes=$(git -C "$dir" -c status.showUntrackedFiles=all status --porcelain --untracked-files=all --ignore-submodules=none | wc -l)"
fi
if [ -f "${VIPER_REBOOT_FILE:-/var/run/reboot-required}" ]; then echo reboot=required; fi
echo "sshd=$(sudo sshd -T 2>/dev/null | grep -iE '^(passwordauthentication|permitrootlogin|authenticationmethods) ' | sort | tr '\n' ';')"
echo "mem=$(free -m | awk '/^Mem:/ {print $7 " MB available of " $2 " MB"}')"
echo "disk=$(df -h / | awk 'NR == 2 {print $4 " free of " $2}')"
