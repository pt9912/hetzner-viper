set -uo pipefail
export LC_ALL=C
dir="$1"; unit="${2:-viper-nats}"; bin="${VIPER_NATS_BIN:-/usr/local/bin/nats-server}"
umask 077
conf="$dir/server.conf"; new="$conf.new"
cat > "$new" || { echo "nats config: cannot write $new"; exit 1; }
# flock: two machines that join at the same moment must not write the file at the same time
exec 9>"$dir/.lock"
flock -w 30 9 || { echo "nats config: another change is running"; rm -f "$new"; exit 1; }
if ! out=$("$bin" -t -c "$new" 2>&1); then
  echo "nats config: the new configuration is not valid, the old one stays:"; echo "$out" | tail -n 5; rm -f "$new"; exit 1
fi
mv "$new" "$conf" || exit 1
if systemctl is-active --quiet "$unit"; then
  sudo systemctl reload "$unit" || { echo "nats config: reload failed"; exit 1; }
  echo "nats config: written, server reloaded"
else
  echo "nats config: written"
fi
