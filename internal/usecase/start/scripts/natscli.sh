set -uo pipefail
export LC_ALL=C
version="$1"; url="$2"; sum="$3"
bin="${VIPER_NATS_CLI:-/usr/local/bin/nats}"
want="${version#v}"
if [ -x "$bin" ] && "$bin" --version 2>/dev/null | grep -qxF "$want"; then
  echo "nats tool: $version is installed"; exit 0
fi
tmp=$(mktemp -d) || exit 1
trap 'rm -rf "$tmp"' EXIT
curl -fsSL --retry 3 --retry-delay 3 --max-time 180 -o "$tmp/nats.zip" "$url" || { echo "nats tool: download failed: $url"; exit 1; }
echo "$sum  $tmp/nats.zip" | sha256sum -c --quiet - 2>/dev/null || { echo "nats tool: the checksum of the download does not match; not installed"; exit 1; }
# python3 is on every Ubuntu server; unzip is not
python3 -m zipfile -e "$tmp/nats.zip" "$tmp/x" >/dev/null 2>&1 || { echo "nats tool: unpacking failed"; exit 1; }
exe=$(find "$tmp/x" -type f -name nats | head -n 1)
[ -n "$exe" ] || { echo "nats tool: no nats program in the download"; exit 1; }
sudo install -m 0755 "$exe" "$bin" || { echo "nats tool: installing $bin failed"; exit 1; }
echo "nats tool: $version installed"
