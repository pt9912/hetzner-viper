brew="$1"; shift
export LC_ALL=C PATH="$HOME/.local/bin:$PATH"
. /etc/os-release 2>/dev/null; echo "os: ${PRETTY_NAME:-?}, kernel $(uname -r)"
echo "apt: $(dpkg-query -W -f='${Package} ${Version}, ' "$@" 2>/dev/null | sed 's/, $//')"
echo "claude: $(claude --version 2>/dev/null | head -n1)"
echo "gh: $(gh --version 2>/dev/null | head -n1)"
echo "docker: $(docker --version 2>/dev/null)"
echo "memory: $(free -m 2>/dev/null | awk '/^Mem:/ {printf "%d MB RAM", $2} /^Swap:/ {printf ", %d MB swap", $2}')"
v=$(code-server --version 2>/dev/null | head -n1); [ -z "$v" ] || echo "code-server: $v"
if [ -x "$brew" ]; then
  eval "$("$brew" shellenv)"; export HOMEBREW_NO_AUTO_UPDATE=1
  echo "homebrew: $(brew --version 2>/dev/null </dev/null | head -n1)"
  echo "brew taps: $(brew tap 2>/dev/null </dev/null | tr '\n' ' ')"
  echo "brew trusted: $(brew trust --json=v1 2>/dev/null </dev/null | jq -c '{taps, formulae, casks, commands} | with_entries(select(.value | length > 0))' 2>/dev/null)"
  leaves=$(brew leaves 2>/dev/null </dev/null)
  echo "brew formulae (installed on request): $( [ -z "$leaves" ] || brew list --versions $leaves 2>/dev/null </dev/null | tr '\n' ',' | sed 's/,$//; s/,/, /g')"
fi
