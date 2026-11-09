set -uo pipefail
export LC_ALL=C
dir="$1"; name="$2"; mode="$3"
umask 077
mkdir -p "$dir" && chmod 700 "$dir" || exit 1
cat > "$dir/$name.new" || exit 1
chmod "$mode" "$dir/$name.new" && mv "$dir/$name.new" "$dir/$name" || { echo "nats file: cannot write $name"; exit 1; }
echo "nats file: $name written"
