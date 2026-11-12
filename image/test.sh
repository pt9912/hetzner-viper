#!/bin/bash
# Tests the image viper-machine with docker, without a cluster: it runs under the same restrictions a Pod has under
# Pod Security "restricted" (uid 1000, no capabilities, no new privileges), and the checks go through ssh with a host
# certificate. usage: image/test.sh [image]   (make image-test)
set -euo pipefail
image="${1:-viper-machine:test}"
here=$(cd "$(dirname "$0")" && pwd)
srv_version=$(sed -n 's/^ARG NATS_SERVER_VERSION=//p' "$here/Dockerfile")
cli_version=$(sed -n 's/^ARG NATS_CLI_VERSION=//p' "$here/Dockerfile")
t=$(mktemp -d); export t; name="viper-machine-test-$$"
trap 'docker rm -f "$name" "$name-2" "$name-3" >/dev/null 2>&1 || true; docker run --rm --user 0 --entrypoint chown -v "$t/home:/h" -v "$t/brew:/b" "$image" -R 1000:1000 /h /b >/dev/null 2>&1 || true; rm -rf "$t"' EXIT
ok=0; bad=0
check() { # check "what" command...
  local what="$1"; shift
  if "$@" >"$t/out" 2>&1; then echo "ok    $what"; ok=$((ok+1)); else echo "FAIL  $what"; sed 's/^/        /' "$t/out" | tail -n 5; bad=$((bad+1)); fi
}

mkdir -p "$t/secret" "$t/home" "$t/brew" && chmod 777 "$t/home" "$t/brew"
# a cluster makes the directory of a volume root's and world-writable: the entrypoint must cope (not owned by builder)
docker run --rm --user 0 --entrypoint chown -v "$t/home:/h" -v "$t/brew:/b" "$image" root:root /h /b
ssh-keygen -q -t ed25519 -N '' -f "$t/ca" -C testca
ssh-keygen -q -t ed25519 -N '' -f "$t/host_key" -C host
ssh-keygen -q -t ed25519 -N '' -f "$t/user" -C user
ssh-keygen -q -s "$t/ca" -I vmtest -h -n vmtest "$t/host_key.pub"
cp "$t/host_key" "$t/host_key.pub" "$t/host_key-cert.pub" "$t/secret/"; cp "$t/user.pub" "$t/secret/authorized_keys"; chmod -R a+rX "$t/secret"
printf '@cert-authority * %s\n' "$(cut -d' ' -f1,2 "$t/ca.pub")" > "$t/known_hosts"

run() { # run NAME PORT HOMEDIR
  docker run -d --name "$1" --user 1000:1000 --cap-drop ALL --security-opt no-new-privileges -e DOCKER_HOST=tcp://127.0.0.1:2375 \
    -p "127.0.0.1:$2:2222" -v "$t/secret:/run/viper/secret:ro" ${3:+-v "$3:/home/builder"} ${4:+-v "$4:/home/linuxbrew"} "$image" >/dev/null
}
sshc() { ssh -p "$1" -i "$t/user" -o IdentitiesOnly=yes -o UserKnownHostsFile="$t/known_hosts" -o StrictHostKeyChecking=yes \
  -o HostKeyAlias=vmtest -o BatchMode=yes -o ConnectTimeout=5 builder@127.0.0.1 "${@:2}"; }
wait_ssh() { for _ in $(seq 1 30); do sshc "$1" true 2>/dev/null && return 0; sleep 1; done; return 1; }

# 1: no volume over the home directory
run "$name" 2222 ""
check "sshd answers and the host certificate is accepted" wait_ssh 2222
check "login as builder (uid 1000)" bash -c "[ \"\$($(declare -f sshc); sshc 2222 'id -un; id -u' | tr '\n' ' ')\" = 'builder 1000 ' ]"
check "the ready file is there" sshc 2222 'test -f /tmp/viper-ready'
check "password login is refused" bash -c "! ssh -p 2222 -o PreferredAuthentications=password -o PubkeyAuthentication=no -o UserKnownHostsFile=$t/known_hosts -o HostKeyAlias=vmtest -o BatchMode=yes builder@127.0.0.1 true"
check "another key is refused" bash -c "ssh-keygen -q -t ed25519 -N '' -f $t/other && ! ssh -p 2222 -i $t/other -o IdentitiesOnly=yes -o UserKnownHostsFile=$t/known_hosts -o HostKeyAlias=vmtest -o BatchMode=yes builder@127.0.0.1 true"
check "a wrong host name is refused" bash -c "! ssh -p 2222 -i $t/user -o IdentitiesOnly=yes -o UserKnownHostsFile=$t/known_hosts -o HostKeyAlias=other -o BatchMode=yes builder@127.0.0.1 true"
check "scp works (sftp subsystem)" bash -c "echo hi > $t/f && scp -P 2222 -i $t/user -o IdentitiesOnly=yes -o UserKnownHostsFile=$t/known_hosts -o HostKeyAlias=vmtest -o BatchMode=yes $t/f builder@127.0.0.1:/tmp/f && $(declare -f sshc) && sshc 2222 'cat /tmp/f' | grep -qx hi"
cfg=$(docker exec "$name" sshd -T -f /home/builder/.viper/sshd/config)
for want in "passwordauthentication no" "kbdinteractiveauthentication no" "pubkeyauthentication yes" "permitrootlogin no" "permitemptypasswords no" "authenticationmethods publickey" "port 2222"; do
  check "effective sshd setting: $want" grep -qx "$want" <<<"$cfg"
done
check "the container runs as uid 1000" bash -c "[ \"\$(docker exec $name id -u)\" = 1000 ]"
check "sudo is not available" bash -c "! docker exec $name sudo -n true"
for tool in git gh make gcc tmux jq docker brew claude; do
  check "tool: $tool" sshc 2222 "bash -lc 'command -v $tool'"
done
check "nats-server is the pinned release ($srv_version)" sshc 2222 "[ \"\$(nats-server --version)\" = 'nats-server: $srv_version' ]"
check "the nats tool is the pinned release ($cli_version)" sshc 2222 "nats --version | grep -qxF '${cli_version#v}'"
check "a nats-server starts as builder and answers the nats tool" sshc 2222 'nats-server -a 127.0.0.1 -p 14222 >/tmp/nats-test.log 2>&1 & p=$!; sleep 1; nats --server nats://127.0.0.1:14222 pub x hi >/dev/null; r=$?; kill $p; exit $r'
# the scripts of viper that install them on a VM must find them and do nothing (they would need sudo to install)
check "viper's natsinstall.sh finds nats-server installed" bash -c "$(declare -f sshc); sshc 2222 \"bash -s -- $srv_version https://x.invalid/x 0\" < $here/../internal/usecase/start/scripts/natsinstall.sh | grep -q 'is installed'"
check "viper's natscli.sh finds the nats tool installed" bash -c "$(declare -f sshc); sshc 2222 \"bash -s -- $cli_version https://x.invalid/x 0\" < $here/../internal/usecase/start/scripts/natscli.sh | grep -q 'is installed'"
check "DOCKER_HOST reaches the sessions of sshd (SetEnv)" sshc 2222 '[ "$DOCKER_HOST" = tcp://127.0.0.1:2375 ]'
check "docker client and plugins" sshc 2222 "bash -lc 'docker --version && docker buildx version && docker compose version'"
check "home was seeded: tmux and VS Code settings" sshc 2222 'test -f ~/.tmux.conf && test -f ~/.vscode-server/data/Machine/settings.json'
check "a work directory below the home directory can be made" sshc 2222 'install -d ~/work/p && touch ~/work/p/x && rm -r ~/work'
check "brew installs a package without root" sshc 2222 "bash -lc 'HOMEBREW_NO_AUTO_UPDATE=1 brew install hello >/dev/null 2>&1 && hello | head -n 1'"
docker rm -f "$name" >/dev/null

# 2: a volume over the home directory (world-writable and empty, as a cluster makes it): the entrypoint brings the files
run "$name-2" 2223 "$t/home" "$t/brew"
check "starts with an empty volume over the home directory" wait_ssh 2223
check "an empty volume is seeded" sshc 2223 'test -f ~/.tmux.conf && test -x ~/.local/bin/claude && test -f ~/.viper-seeded'
check "claude runs from the seeded home" sshc 2223 "bash -lc 'claude --version'"
check "Homebrew was put on its volume, and what brew installs there survives a new container" bash -c "$(declare -f sshc)
  sshc 2223 'bash -lc \"HOMEBREW_NO_AUTO_UPDATE=1 brew install hello >/dev/null 2>&1 && hello | head -n 1\"' | grep -q Hello &&
  docker rm -f $name-2 >/dev/null && docker run -d --name $name-2 --user 1000:1000 --cap-drop ALL --security-opt no-new-privileges -p 127.0.0.1:2223:2222 \
    -v $t/secret:/run/viper/secret:ro -v $t/home:/home/builder -v $t/brew:/home/linuxbrew $image >/dev/null &&
  $(declare -f wait_ssh) && wait_ssh 2223 && sshc 2223 'bash -lc \"hello | head -n 1\"' | grep -q Hello"
check "a change in the volume survives a restart" bash -c "$(declare -f sshc); sshc 2223 'echo mine > ~/keep.txt' && docker restart $name-2 >/dev/null && $(declare -f wait_ssh) && wait_ssh 2223 && sshc 2223 'cat ~/keep.txt' | grep -qx mine"

docker rm -f "$name-2" >/dev/null

# 3: the pod is the NATS server of a group: nats-server runs as a process (no systemd), reads the address of the pod from the
# environment, is reloaded by a signal and started again when it ends
docker run -d --name "$name-3" --user 1000:1000 --cap-drop ALL --security-opt no-new-privileges -e VIPER_NATS_SERVER=1 -e POD_IP=127.0.0.1 \
  -p 127.0.0.1:2224:2222 -v "$t/secret:/run/viper/secret:ro" "$image" >/dev/null
check "a server pod starts" wait_ssh 2224
check "POD_IP reaches the sessions of sshd" sshc 2224 '[ "$POD_IP" = 127.0.0.1 ]'
check "nats-server starts when the configuration is there and listens on the address of the pod" sshc 2224 '
  d=~/.viper-nats; mkdir -p $d; chmod 700 $d
  printf "host: \$POD_IP\nport: 14223\n" > $d/server.conf
  openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -keyout $d/server.key -out $d/server.crt -days 1 -subj /CN=t >/dev/null 2>&1
  for _ in $(seq 1 20); do [ -s $d/nats-server.pid ] && ss -ltn "sport = :14223" | grep -qF 127.0.0.1:14223 && exit 0; sleep 1; done; exit 1'
check "a signal makes it read the configuration again (the process stays, the log says it was read)" sshc 2224 '
  d=~/.viper-nats; p=$(cat $d/nats-server.pid); kill -HUP $p; sleep 2; kill -0 $p && [ "$(cat $d/nats-server.pid)" = "$p" ] &&
  tail -n 3 $d/nats-server.log | grep -q "Reloaded server configuration" && ! tail -n 3 $d/nats-server.log | grep -q "Failed to reload"'
check "nats-server is started again after it ended" sshc 2224 '
  d=~/.viper-nats; p=$(cat $d/nats-server.pid); kill -9 $p; for _ in $(seq 1 20); do n=$(cat $d/nats-server.pid 2>/dev/null); [ -n "$n" ] && [ "$n" != "$p" ] && kill -0 $n && exit 0; sleep 1; done; exit 1'
check "viper's natsconfig.sh reloads it through the pid file" bash -c "$(declare -f sshc)
  script=\$(cat $here/../internal/usecase/natsgroup/scripts/natsconfig.sh)
  printf 'host: \$POD_IP\nport: 14223\n' | sshc 2224 \"bash -c \$(printf %q \"\$script\") -- /home/builder/.viper-nats viper-nats\" | grep -q 'server reloaded'"

echo "--- $ok ok, $bad failed"
[ "$bad" -eq 0 ]
