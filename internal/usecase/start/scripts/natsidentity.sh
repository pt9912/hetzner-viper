set -uo pipefail
export LC_ALL=C
dir="$1"
umask 077
mkdir -p "$dir" && chmod 700 "$dir" || exit 1
# stdin: the seed on the first line, the public key on the second. A machine keeps its identity.
if [ -s "$dir/client.nk" ] && [ -s "$dir/client.pub" ]; then
  echo "nats identity: kept"; cat "$dir/client.pub"; exit 0
fi
{ IFS= read -r seed && IFS= read -r pub; } || { echo "nats identity: no key on stdin"; exit 1; }
case "$seed" in SU*) ;; *) echo "nats identity: not a user seed"; exit 1 ;; esac
printf '%s\n' "$seed" > "$dir/client.nk.new" && printf '%s\n' "$pub" > "$dir/client.pub.new" \
  && chmod 600 "$dir/client.nk.new" && chmod 644 "$dir/client.pub.new" \
  && mv "$dir/client.nk.new" "$dir/client.nk" && mv "$dir/client.pub.new" "$dir/client.pub" || { echo "nats identity: cannot write the files"; exit 1; }
echo "nats identity: created"; echo "$pub"
