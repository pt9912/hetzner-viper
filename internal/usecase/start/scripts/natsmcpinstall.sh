set -uo pipefail
export LC_ALL=C
dir="$1"; want="$2"
umask 077
mkdir -p "$dir" && chmod 700 "$dir" || exit 1
# stdin: the binary. It is written next to its place, checked against the sum viper computed, then moved.
cat > "$dir/viper-nats-mcp.new" || { echo "nats mcp: cannot write the binary"; exit 1; }
got=$(sha256sum "$dir/viper-nats-mcp.new" | cut -d' ' -f1)
if [ "$got" != "$want" ]; then
  rm -f "$dir/viper-nats-mcp.new"
  echo "nats mcp: the checksum of the binary on the machine ($got) is not the one sent ($want)"; exit 1
fi
chmod 755 "$dir/viper-nats-mcp.new" && mv "$dir/viper-nats-mcp.new" "$dir/viper-nats-mcp" || { echo "nats mcp: cannot place the binary"; exit 1; }
echo "nats mcp: $("$dir/viper-nats-mcp" --version 2>&1 | head -n 1) installed"
