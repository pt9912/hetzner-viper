set -uo pipefail
export LC_ALL=C
# The bucket of the object store (NATS_OBJECTS) is made on the machine of the server, with the key of the entry viper-admin that
# only this machine has. A server that was made without an object store has no such key: nothing to do.
dir="$1"; shift
if [ ! -s "$dir/admin.nk" ]; then
  echo "nats bucket: this server has no object store (NATS_OBJECTS was off when it was made)"; exit 0
fi
exec "$dir/viper-nats-mcp" ensure-bucket "$@"
