set -uo pipefail
export LC_ALL=C
ip="$1"; port="$2"; objects="${3:-off}"
# the server speaks first: INFO with the flags that say it wants TLS and a login
# a Service name answers a moment after the pod is ready: try for a while
ok=''
for _ in $(seq 1 30); do
  if exec 3<>"/dev/tcp/$ip/$port" 2>/dev/null; then ok=1; break; fi
  sleep 1
done
[ -n "$ok" ] || { echo "nats check: cannot connect to $ip:$port"; exit 1; }
IFS= read -r -t 5 -u 3 line || { echo "nats check: no answer from $ip:$port"; exit 1; }
case "$line" in INFO\ *) ;; *) echo "nats check: not a NATS server: ${line:0:40}"; exit 1 ;; esac
case "$line" in *'"tls_required":true'*) ;; *) echo "nats check: the server does not require TLS"; exit 1 ;; esac
case "$line" in *'"auth_required":true'*) ;; *) echo "nats check: the server does not require a login"; exit 1 ;; esac
# JetStream is off, except for a group with an object store (NATS_OBJECTS), where it must be on
js=''; case "$line" in *'"jetstream":true'*|*'"jetstream": true'*) js=1 ;; esac
if [ "$objects" = on ]; then
  [ -n "$js" ] || { echo "nats check: JetStream is off, the group has an object store"; exit 1; }
elif [ -n "$js" ]; then
  echo "nats check: JetStream is on, it must not be"; exit 1
fi
echo "nats check: $ip:$port answers, TLS and login are required"
