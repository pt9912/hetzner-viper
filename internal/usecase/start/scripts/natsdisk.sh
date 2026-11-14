set -uo pipefail
export LC_ALL=C
# The object store of the group (NATS_OBJECTS) needs room: its largest size plus a reserve for the system and the logs.
# The directory may not exist yet: the nearest one that does says how much room there is.
dir="$1"; need="$2"
case "$need" in ''|*[!0-9]*) echo "nats disk: the size is no number: $need"; exit 1 ;; esac
p="$dir"
while [ ! -d "$p" ] && [ "$p" != / ]; do p=$(dirname "$p"); done
avail=$(df --output=avail -B1 "$p" 2>/dev/null | tail -n 1 | tr -d ' ')
case "$avail" in ''|*[!0-9]*) echo "nats disk: cannot read the free space of $p"; exit 1 ;; esac
if [ "$avail" -lt "$need" ]; then
  echo "nats disk: $((avail / 1048576)) MiB free at $p, the object store needs $((need / 1048576)) MiB (NATS_OBJECTS_MAX plus 1 GiB): lower NATS_OBJECTS_MAX or use a larger disk"
  exit 1
fi
echo "nats disk: $((avail / 1048576)) MiB free at $p"
