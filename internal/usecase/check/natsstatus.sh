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
  echo "unit=$(systemctl is-active viper-nats 2>/dev/null || true)"
  echo "listen=$(ss -ltn "sport = :$port" 2>/dev/null | awk 'NR > 1 {print $4}' | paste -sd, -)"
  echo "registered=$(grep '^# viper-client ' "$dir/server.conf" | awk '{print $3}' | paste -sd, -)"
fi
