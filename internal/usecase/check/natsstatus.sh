# nats_status: read-only look at the NATS side of a machine; it prints key=value lines and changes nothing
set -uo pipefail
export LC_ALL=C
dir="$1"
if [ ! -s "$dir/connection.env" ]; then echo "env=missing"; exit 0; fi
echo "env=ok"
set -a; . "$dir/connection.env"; set +a
echo "url=$NATS_URL"
crt="$NATS_CA_FILE"
if [ -s "$crt" ]; then
  end=$(openssl x509 -in "$crt" -noout -enddate 2>/dev/null | cut -d= -f2)
  if [ -n "$end" ] && epoch=$(date -d "$end" +%s 2>/dev/null); then echo "cert_days=$(( (epoch - $(date +%s)) / 86400 ))"; else echo "cert_days=unreadable"; fi
  echo "cert_ip=$(openssl x509 -in "$crt" -noout -ext subjectAltName 2>/dev/null | grep -o 'IP Address:[0-9.]*' | head -n 1 | cut -d: -f2)"
  echo "cert_dns=$(openssl x509 -in "$crt" -noout -ext subjectAltName 2>/dev/null | grep -o 'DNS:[^, ]*' | head -n 1 | cut -d: -f2)"
else
  echo "cert_days=missing"
fi
if command -v nats >/dev/null 2>&1; then
  # a publish to the machine's own subject fails (exit status 1) if the connection, the certificate or the
  # key is refused; nats rtt does not (it prints the failure and exits 0)
  if out=$(timeout 20 nats --server "$NATS_URL" --tlsca "$NATS_CA_FILE" --nkey "$NATS_NKEY_SEED_FILE" pub "${VIPER_NATS_SUBJECT_PREFIX}.login-test" "viper check" 2>&1); then
    echo "login=ok"
  else
    echo "login=failed"; echo "login_err=$(echo "$out" | tail -n 1 | tr '\n' ' ')"
  fi
else
  echo "login=no-tool"
fi
if [ -s "$dir/server.conf" ]; then
  echo "server=yes"
  port="${NATS_URL##*:}"
  # a pod has no systemd: its server is a process of the container, with a pid file
  if [ -s "$dir/nats-server.pid" ]; then
    pid=$(cat "$dir/nats-server.pid")
    if tr '\0' ' ' < "/proc/$pid/cmdline" 2>/dev/null | grep -q nats-server; then echo "unit=active"; else echo "unit=inactive"; fi
  else
    echo "unit=$(systemctl is-active viper-nats 2>/dev/null || true)"
  fi
  echo "listen=$(ss -ltn "sport = :$port" 2>/dev/null | awk 'NR > 1 {print $4}' | paste -sd, -)"
  # v0.15.0 wrote allow_responses, which lets any machine publish anywhere in the group
  echo "allow_responses=$(grep -c allow_responses "$dir/server.conf")"
  echo "registered=$(grep '^# viper-client ' "$dir/server.conf" | awk '{print $3}' | paste -sd, -)"
  echo "operators=$(grep '^# viper-operator ' "$dir/server.conf" | awk '{print $3 ":" $4}' | paste -sd, -)"
  # the object store (NATS_OBJECTS): the line viper wrote, and whether the running server has JetStream
  echo "objects=$(sed -n 's/^# viper-objects //p' "$dir/server.conf" | head -n 1)"
fi
# the MCP server (NATS_MCP): the binary, its entry with Claude Code, the push list and the allowed tools
if [ -x "$dir/viper-nats-mcp" ]; then
  echo "mcp_bin=ok"
  echo "mcp_version=$("$dir/viper-nats-mcp" --version 2>&1 | head -n 1)"
  echo "mcp_sha=$(sha256sum "$dir/viper-nats-mcp" 2>/dev/null | cut -d' ' -f1)"
  echo "mcp_arch=$(uname -m)"
else
  echo "mcp_bin=missing"
fi
if command -v jq >/dev/null 2>&1; then
  echo "mcp_claude=$(jq -r 'if .mcpServers.nats then (.mcpServers.nats.command // "") else "none" end' "$HOME/.claude.json" 2>/dev/null || echo none)"
  echo "mcp_allowed=$(jq -r '[.permissions.allow[]? | select(startswith("mcp__nats__")) | sub("^mcp__nats__"; "")] | join(",")' "$HOME/.claude/settings.json" 2>/dev/null)"
else
  echo "mcp_claude=no-jq"
fi
echo "mcp_push=$(sed -n 's/^VIPER_NATS_MCP_PUSH_FROM=//p' "$dir/mcp.env" 2>/dev/null | head -n 1)"
echo "mcp_topics=$(sed -n 's/^VIPER_NATS_MCP_TOPICS=//p' "$dir/mcp.env" 2>/dev/null | head -n 1)"
