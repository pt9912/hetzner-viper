set -uo pipefail
export LC_ALL=C
# The NATS server of a pod: there is no systemd, the entrypoint of the container runs nats-server as a process
# (image/nats-supervisor.sh) as soon as server.conf is there and starts it again if it ends. This waits until it listens.
dir="$1"; ip="$2"; port="$3"
pidfile="$dir/nats-server.pid"
for _ in $(seq 1 40); do
  if [ -s "$pidfile" ] && kill -0 "$(cat "$pidfile")" 2>/dev/null && ss -ltn "sport = :$port" 2>/dev/null | grep -qF "$ip:$port"; then
    echo "nats process: running, listening on $ip:$port"; exit 0
  fi
  sleep 1
done
echo "nats process: not listening on $ip:$port after 40 seconds"; tail -n 8 "$dir/nats-server.log" 2>/dev/null; exit 1
