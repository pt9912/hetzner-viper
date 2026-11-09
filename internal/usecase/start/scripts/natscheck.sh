set -uo pipefail
export LC_ALL=C
ip="$1"; port="$2"
# the server speaks first: INFO with the flags that say it wants TLS and a login
exec 3<>"/dev/tcp/$ip/$port" 2>/dev/null || { echo "nats check: cannot connect to $ip:$port"; exit 1; }
IFS= read -r -t 5 -u 3 line || { echo "nats check: no answer from $ip:$port"; exit 1; }
case "$line" in INFO\ *) ;; *) echo "nats check: not a NATS server: ${line:0:40}"; exit 1 ;; esac
case "$line" in *'"tls_required":true'*) ;; *) echo "nats check: the server does not require TLS"; exit 1 ;; esac
case "$line" in *'"auth_required":true'*) ;; *) echo "nats check: the server does not require a login"; exit 1 ;; esac
case "$line" in *'"jetstream":true'*|*'"jetstream": true'*) echo "nats check: JetStream is on, it must not be"; exit 1 ;; esac
echo "nats check: $ip:$port answers, TLS and login are required"
