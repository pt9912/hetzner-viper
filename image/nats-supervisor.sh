#!/bin/bash
# nats-server of a pod (the server of a NATS group). There is no systemd in a container: the entrypoint starts this when
# the pod is the server (VIPER_NATS_SERVER=1). It runs nats-server as soon as viper has written the configuration and the
# certificate (viper up, over ssh), and starts it again when it ends. viper reloads the configuration with SIGHUP
# (the pid is in the pid file) and looks at the log.
set -u
dir=/home/builder/.viper-nats
umask 077
rm -f "$dir/nats-server.pid" # a pid file that survived a restart of the container names a process that is gone
while true; do
  if [ -s "$dir/server.conf" ] && [ -s "$dir/server.crt" ] && [ -s "$dir/server.key" ]; then
    # POD_IP, the address of the pod, is in the environment of the container: the configuration says "host: $POD_IP"
    # no --log_size_limit: nats-server then refuses every reload ("config reload not supported for LogSizeLimit"), and the
    # new users would never be read. The log is kept small here instead: an old one is put aside at each start.
    if [ "$(stat -c %s "$dir/nats-server.log" 2>/dev/null || echo 0)" -gt 5242880 ]; then mv -f "$dir/nats-server.log" "$dir/nats-server.log.1"; fi
    nats-server -c "$dir/server.conf" --pid "$dir/nats-server.pid" -l "$dir/nats-server.log"
    rm -f "$dir/nats-server.pid"
  fi
  sleep 2
done
