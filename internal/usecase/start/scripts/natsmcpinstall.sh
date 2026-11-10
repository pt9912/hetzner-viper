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
chmod 755 "$dir/viper-nats-mcp.new" || { rm -f "$dir/viper-nats-mcp.new"; echo "nats mcp: cannot make the binary executable"; exit 1; }
# it must run here (the right architecture) before it replaces the one that works
if ! ver=$("$dir/viper-nats-mcp.new" --version 2>&1 | head -n 1) || [ -z "$ver" ] || [ "${ver#viper-nats-mcp}" = "$ver" ]; then
  rm -f "$dir/viper-nats-mcp.new"
  echo "nats mcp: the binary does not run on this machine (wrong architecture?): $ver"; exit 1
fi
mv "$dir/viper-nats-mcp.new" "$dir/viper-nats-mcp" || { echo "nats mcp: cannot place the binary"; exit 1; }
echo "nats mcp: $ver installed"
