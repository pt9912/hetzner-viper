#!/bin/bash
# Entrypoint of viper-machine. Runs as builder, without root.
# The material of the machine comes as files, never in the environment or the command line:
#   $VIPER_SECRET_DIR/authorized_keys      the keys that may log in
#   $VIPER_SECRET_DIR/host_key             the host key of the machine
#   $VIPER_SECRET_DIR/host_key.pub         its public key (viper pins it)
#   $VIPER_SECRET_DIR/host_key-cert.pub    its certificate (signed by the host CA)
set -euo pipefail
secret="${VIPER_SECRET_DIR:-/run/viper/secret}"
home=/home/builder
ready="${VIPER_READY_FILE:-/tmp/viper-ready}"

fail() { echo "viper-machine: $*" >&2; exit 1; }
for f in authorized_keys host_key host_key.pub host_key-cert.pub; do
  [ -s "$secret/$f" ] || fail "$secret/$f is missing"
done

# A volume over the home directory hides what the image has there: bring it once, and never overwrite. The directory
# of the volume is not ours (a cluster makes it root's, world-writable), so its own mode and times are left alone:
# the entries are copied one by one, not "." itself.
if [ ! -e "$home/.viper-seeded" ]; then
  shopt -s dotglob nullglob
  cp -r --update=none --preserve=mode,timestamps,links /opt/viper/home-seed/* "$home/"
  : > "$home/.viper-seeded"
fi
# the volume made the directory; its owner and mode are not ours to set when we are not root, only its content
mkdir -p "$home/.ssh" "$home/.viper/sshd"
chmod 700 "$home/.ssh" "$home/.viper" "$home/.viper/sshd"
install -m 600 "$secret/authorized_keys" "$home/.ssh/authorized_keys"
install -m 600 "$secret/host_key" "$home/.viper/sshd/host_key"
install -m 644 "$secret/host_key.pub" "$home/.viper/sshd/host_key.pub"
install -m 644 "$secret/host_key-cert.pub" "$home/.viper/sshd/host_key-cert.pub"

# The environment of the container does not reach the sessions of sshd. What the sessions need of it is handed over by
# SetEnv: where the Docker daemon is (the sidecar or a remote one), and the address of the pod (the NATS server reads it
# when viper tests its configuration over ssh).
cfg="$home/.viper/sshd/config"
cp /opt/viper/sshd_config "$cfg"
setenv=""
for name in DOCKER_HOST POD_IP; do
  value="${!name:-}"
  [ -n "$value" ] || continue
  case "$value" in *[[:space:]\"]*) fail "$name has a blank or a quote in it" ;; esac
  setenv="$setenv $name=$value"
done
[ -z "$setenv" ] || echo "SetEnv$setenv" >> "$cfg"

# the pod is the NATS server of its group: nats-server runs here as a process, without systemd
if [ "${VIPER_NATS_SERVER:-}" = 1 ]; then
  /opt/viper/nats-supervisor.sh &
fi

: > "$ready"
exec /usr/sbin/sshd -D -e -f "$cfg"
