set -uo pipefail
export LC_ALL=C
dir="$1"
set -a; . "$dir/connection.env"; set +a
# a publish to the machine's own subject: it fails (exit status 1) if the connection, the certificate
# or the key is refused. (nats rtt does not: it prints the failure and exits 0.)
if out=$(timeout 20 nats --server "$NATS_URL" --tlsca "$NATS_CA_FILE" --nkey "$NATS_NKEY_SEED_FILE" pub "${VIPER_NATS_SUBJECT_PREFIX}.login-test" "viper up" 2>&1); then
  echo "nats check: logged in over TLS with the NKey at $NATS_URL"
else
  echo "nats check: the login to $NATS_URL failed:"; echo "$out" | tail -n 4; exit 1
fi
