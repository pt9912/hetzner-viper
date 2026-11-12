set -uo pipefail
export LC_ALL=C
dir="$1"; unit="${2:-viper-nats}"; expect="${3:-}"; bin="${VIPER_NATS_BIN:-/usr/local/bin/nats-server}"
umask 077
conf="$dir/server.conf"
# a file of its own: machines that join at the same moment write at the same moment, and a common name
# would let one replace what the other has just checked
new=$(mktemp "$conf.XXXXXX") || { echo "nats config: cannot make a temporary file"; exit 1; }
trap 'rm -f "$new"' EXIT
cat > "$new" || { echo "nats config: cannot write $new"; exit 1; }
# flock: machines that join at the same moment must not write the file at the same time
exec 9>"$dir/.lock"
flock -w 30 9 || { echo "nats config: another change is running"; rm -f "$new"; exit 1; }
if [ -n "$expect" ]; then
  now=none; [ -s "$conf" ] && now=$(sha256sum "$conf" | cut -d' ' -f1)
  if [ "$now" != "$expect" ]; then echo "nats config: the file changed meanwhile"; rm -f "$new"; exit 3; fi
fi
if ! out=$("$bin" -t -c "$new" 2>&1); then
  echo "nats config: the new configuration is not valid, the old one stays:"; echo "$out" | tail -n 5; rm -f "$new"; exit 1
fi
mv "$new" "$conf" || exit 1
pidfile="$dir/nats-server.pid"
if [ -s "$pidfile" ] && kill -0 "$(cat "$pidfile")" 2>/dev/null; then
  # a pod: nats-server is a process of the container (no systemd), the signal makes it read the file again
  kill -HUP "$(cat "$pidfile")" || { echo "nats config: reload failed"; exit 1; }
  echo "nats config: written, server reloaded"
elif command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet "$unit"; then
  sudo systemctl reload "$unit" || { echo "nats config: reload failed"; exit 1; }
  echo "nats config: written, server reloaded"
else
  echo "nats config: written"
fi
