set -uo pipefail
export LC_ALL=C
unit="$1"; ip="$2"; port="$3"
tmp=$(mktemp) || exit 1
trap 'rm -f "$tmp"' EXIT
cat > "$tmp" || exit 1
if ! sudo cmp -s "$tmp" "/etc/systemd/system/$unit.service"; then
  sudo install -m 0644 "$tmp" "/etc/systemd/system/$unit.service" || { echo "nats unit: cannot write the unit"; exit 1; }
  sudo systemctl daemon-reload || exit 1
fi
sudo systemctl enable "$unit" >/dev/null 2>&1 || { echo "nats unit: enable failed"; exit 1; }
sudo systemctl start "$unit" || { echo "nats unit: start failed"; sudo systemctl status "$unit" --no-pager 2>&1 | tail -n 8; exit 1; }
for _ in $(seq 1 30); do
  if ss -ltn "sport = :$port" 2>/dev/null | grep -qF "$ip:$port"; then echo "nats unit: running, listening on $ip:$port"; exit 0; fi
  sleep 1
done
echo "nats unit: not listening on $ip:$port after 30 seconds"; sudo journalctl -u "$unit" --no-pager -n 8 2>&1 | tail -n 8; exit 1
