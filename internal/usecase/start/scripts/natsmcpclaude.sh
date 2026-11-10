set -uo pipefail
export LC_ALL=C
export PATH="$HOME/.local/bin:$PATH"
umask 077
bin="$1"; allow="$2"
command -v claude >/dev/null 2>&1 || { echo "nats mcp: Claude Code is not installed on the machine"; exit 1; }
# an entry of an earlier run is replaced, not doubled (it is not an error if there is none)
claude mcp remove --scope user nats >/dev/null 2>&1
out=$(claude mcp add --scope user nats -- "$bin" 2>&1) || { echo "nats mcp: claude mcp add failed:"; echo "$out" | tail -n 4; exit 1; }
echo "nats mcp: registered with Claude Code as nats (user scope)"
# the tools viper lets the agent use without asking: only those of this server, and only as listed.
# An earlier list is replaced, so a smaller NATS_MCP_AUTOALLOW takes the rights away again.
f="$HOME/.claude/settings.json"
if [ -z "$allow" ] && [ ! -f "$f" ]; then exit 0; fi
if ! command -v jq >/dev/null 2>&1; then
  if [ -z "$allow" ] && ! grep -q 'mcp__nats__' "$f" 2>/dev/null; then exit 0; fi
  echo "nats mcp: jq is missing, so the tools could not be allowed or taken away"; exit 2
fi
mkdir -p "$HOME/.claude"
[ -f "$f" ] || echo '{}' > "$f"
list=$(printf '%s\n' "$allow" | jq -Rc 'split(",") | map(select(length > 0) | "mcp__nats__" + .)')
tmp=$(mktemp "$f.XXXXXX") || { echo "nats mcp: cannot make a temporary file"; exit 1; }
trap 'rm -f "$tmp"' EXIT
# written back into the file itself (not moved over it): a link into a dotfiles repository stays, and so do the rights
if jq --argjson add "$list" '.permissions.allow = (((.permissions.allow // []) | map(select(startswith("mcp__nats__") | not))) + $add)' "$f" > "$tmp" \
  && [ -s "$tmp" ] && cat "$tmp" > "$f"; then :; else echo "nats mcp: ~/.claude/settings.json could not be changed"; exit 1; fi
if [ -n "$allow" ]; then echo "nats mcp: allowed without asking: $allow"; fi
