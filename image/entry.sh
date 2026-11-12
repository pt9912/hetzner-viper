#!/bin/bash
# Entrypoint of viper-machine. Runs as builder, without root.
# The material of the machine comes as files, never in the environment or the command line:
#   $VIPER_SECRET_DIR/authorized_keys      the keys that may log in
#   $VIPER_SECRET_DIR/host_key             the host key of the machine
#   $VIPER_SECRET_DIR/host_key-cert.pub    its certificate (signed by the host CA)
set -euo pipefail
secret="${VIPER_SECRET_DIR:-/run/viper/secret}"
home=/home/builder
ready="${VIPER_READY_FILE:-/tmp/viper-ready}"

fail() { echo "viper-machine: $*" >&2; exit 1; }
for f in authorized_keys host_key host_key-cert.pub; do
  [ -s "$secret/$f" ] || fail "$secret/$f is missing"
done

# A volume over the home directory hides what the image has there: bring it once, and never overwrite.
if [ ! -e "$home/.viper-seeded" ]; then
  cp -an /opt/viper/home-seed/. "$home/"
  : > "$home/.viper-seeded"
fi
# the volume made the directory; its owner and mode are not ours to set when we are not root, only its content
mkdir -p "$home/.ssh" "$home/.viper/sshd"
chmod 700 "$home/.ssh" "$home/.viper" "$home/.viper/sshd"
install -m 600 "$secret/authorized_keys" "$home/.ssh/authorized_keys"
install -m 600 "$secret/host_key" "$home/.viper/sshd/host_key"
install -m 644 "$secret/host_key-cert.pub" "$home/.viper/sshd/host_key-cert.pub"

: > "$ready"
exec /usr/sbin/sshd -D -e -f /opt/viper/sshd_config
