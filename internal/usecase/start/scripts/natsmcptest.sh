set -uo pipefail
export LC_ALL=C
bin="$1"
# the server is started like Claude Code starts it: initialize, then the list of tools, over stdio.
# It must answer with its tools; then stdin ends and it stops.
req='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"viper","version":"1"}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
out=$( { printf '%s\n' "$req"; sleep 3; } | timeout 20 "$bin" 2>&1 )
if echo "$out" | grep -q '"name":"peers"' && echo "$out" | grep -q '"name":"send"'; then
  echo "nats mcp: the server answers over stdio and offers its tools"
else
  echo "nats mcp: the server did not answer as expected:"; echo "$out" | tail -n 5; exit 1
fi
