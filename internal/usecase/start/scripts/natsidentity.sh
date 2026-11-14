set -uo pipefail
export LC_ALL=C
dir="$1"; name="${2:-client}"  # client: the machine; admin: the entry that makes the bucket (server only)
umask 077
mkdir -p "$dir" && chmod 700 "$dir" || exit 1
# stdin: the seed on the first line, the public key on the second. A machine keeps its identity.
if [ -s "$dir/$name.nk" ] && [ -s "$dir/$name.pub" ]; then
  echo "nats identity: kept"; cat "$dir/$name.pub"; exit 0
fi
{ IFS= read -r seed && IFS= read -r pub; } || { echo "nats identity: no key on stdin"; exit 1; }
case "$seed" in SU*) ;; *) echo "nats identity: not a user seed"; exit 1 ;; esac
printf '%s\n' "$seed" > "$dir/$name.nk.new" && printf '%s\n' "$pub" > "$dir/$name.pub.new" \
  && chmod 600 "$dir/$name.nk.new" && chmod 644 "$dir/$name.pub.new" \
  && mv "$dir/$name.nk.new" "$dir/$name.nk" && mv "$dir/$name.pub.new" "$dir/$name.pub" || { echo "nats identity: cannot write the files"; exit 1; }
echo "nats identity: created"; echo "$pub"
