set -uo pipefail
export LC_ALL=C
dir="$1"
conf="$dir/server.conf"
# the first line says which version of the file this is; a writer gives it back to be sure that
# nobody changed the file in between
if [ -s "$conf" ]; then echo "sha256 $(sha256sum "$conf" | cut -d' ' -f1)"; cat "$conf"; else echo "sha256 none"; fi
