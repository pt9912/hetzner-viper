set -uo pipefail
export LC_ALL=C
version="$1"; url="$2"; sum="$3"
bin="${VIPER_NATS_BIN:-/usr/local/bin/nats-server}"
if [ -x "$bin" ] && [ "$("$bin" --version 2>/dev/null)" = "nats-server: $version" ]; then
  echo "nats-server: $version is installed"; exit 0
fi
tmp=$(mktemp -d) || exit 1
trap 'rm -rf "$tmp"' EXIT
curl -fsSL --retry 3 --retry-delay 3 --max-time 180 -o "$tmp/nats.tgz" "$url" || { echo "nats-server: download failed: $url"; exit 1; }
echo "$sum  $tmp/nats.tgz" | sha256sum -c --quiet - 2>/dev/null || { echo "nats-server: the checksum of the download does not match; not installed"; exit 1; }
tar -xzf "$tmp/nats.tgz" -C "$tmp" --strip-components=1 --wildcards '*/nats-server' || { echo "nats-server: unpacking failed"; exit 1; }
sudo install -m 0755 "$tmp/nats-server" "$bin" || { echo "nats-server: installing $bin failed"; exit 1; }
echo "nats-server: $version installed"
