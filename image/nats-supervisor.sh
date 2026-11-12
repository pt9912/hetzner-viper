#!/bin/bash
# nats-server of a pod (the server of a NATS group). There is no systemd in a container: the entrypoint starts this when
# the pod is the server (VIPER_NATS_SERVER=1). It runs nats-server as soon as viper has written the configuration and the
# certificate (viper up, over ssh), and starts it again when it ends. viper reloads the configuration with SIGHUP
# (the pid is in the pid file) and looks at the log.
set -u
dir=/home/builder/.viper-nats
umask 077
while true; do
  if [ -s "$dir/server.conf" ] && [ -s "$dir/server.crt" ] && [ -s "$dir/server.key" ]; then
    # POD_IP, the address of the pod, is in the environment of the container: the configuration says "host: $POD_IP"
    nats-server -c "$dir/server.conf" --pid "$dir/nats-server.pid" -l "$dir/nats-server.log" --log_size_limit 5242880
    rm -f "$dir/nats-server.pid"
  fi
  sleep 2
done
