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
  # the marker is written last, so a start that comes here again was interrupted or is the first: nothing of the user
  # is there yet, and what a cut-off copy left is overwritten
  cp -r --preserve=mode,timestamps,links /opt/viper/home-seed/* "$home/"
  : > "$home/.viper-seeded"
fi
# Homebrew lives in /home/linuxbrew, which is a volume of its own in a pod (what brew installs must survive a new pod):
# the first start puts the Homebrew of the image there. The same care for the directory of the volume as above.
brewdir=/home/linuxbrew
if [ ! -e "$brewdir/.viper-seeded" ] && [ -d /opt/viper/brew-seed ]; then
  echo "viper-machine: putting Homebrew on its volume (first start) ..." >&2
  # into a place of its own, then renamed: a copy that is cut off never looks like a Homebrew
  rm -rf "$brewdir/.linuxbrew.partial" "$brewdir/.linuxbrew"
  cp -r --preserve=mode,timestamps,links /opt/viper/brew-seed "$brewdir/.linuxbrew.partial" &&
    mv "$brewdir/.linuxbrew.partial" "$brewdir/.linuxbrew" ||
    fail "cannot put Homebrew on $brewdir (is the volume writable for group 1000?)"
  : > "$brewdir/.viper-seeded"
fi
# the volume made the directory; its owner and mode are not ours to set when we are not root, only its content
mkdir -p "$home/.ssh" "$home/.viper/sshd"
chmod 700 "$home/.ssh" "$home/.viper" "$home/.viper/sshd"
install -m 600 "$secret/authorized_keys" "$home/.ssh/authorized_keys"
install -m 600 "$secret/host_key" "$home/.viper/sshd/host_key"
install -m 644 "$secret/host_key.pub" "$home/.viper/sshd/host_key.pub"
install -m 644 "$secret/host_key-cert.pub" "$home/.viper/sshd/host_key-cert.pub"

# A container (Docker Compose) has no Downward API: the server of a NATS group is told the subnet of the group network
# (VIPER_NATS_SUBNET) and takes the address it has in it. hostname -i is not reliable with two networks. The address may
# come a moment after the start.
if [ -z "${POD_IP:-}" ] && [ -n "${VIPER_NATS_SUBNET:-}" ]; then
  for _ in $(seq 1 60); do
    POD_IP=$(python3 - "$VIPER_NATS_SUBNET" <<'PY'
import ipaddress, re, sys
net = ipaddress.ip_network(sys.argv[1], strict=False)
for m in re.finditer(r"\|-- (\S+)\n\s+/32 host LOCAL", open("/proc/net/fib_trie").read()):
    if ipaddress.ip_address(m.group(1)) in net:
        print(m.group(1))
        break
PY
) || POD_IP=""
    [ -z "$POD_IP" ] || break
    sleep 1
  done
  [ -n "$POD_IP" ] || fail "no address in $VIPER_NATS_SUBNET"
  export POD_IP
fi

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
# The only locale of the image is C.UTF-8. A client sends its own (de_DE.UTF-8 with SendEnv LANG LC_*), which is not there and
# leaves btop, tmux and others in ASCII ("No UTF-8 locale detected"); SetEnv has the last word over what the client sends.
setenv="$setenv LANG=C.UTF-8 LC_ALL=C.UTF-8"
[ -z "$setenv" ] || echo "SetEnv$setenv" >> "$cfg"

# the pod is the NATS server of its group: nats-server runs here as a process, without systemd
if [ "${VIPER_NATS_SERVER:-}" = 1 ]; then
  /opt/viper/nats-supervisor.sh &
fi

: > "$ready"
exec /usr/sbin/sshd -D -e -f "$cfg"
