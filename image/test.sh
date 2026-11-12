#!/bin/bash
# Tests the image viper-machine with docker, without a cluster: it runs under the same restrictions a Pod has under
# Pod Security "restricted" (uid 1000, no capabilities, no new privileges), and the checks go through ssh with a host
# certificate. usage: image/test.sh [image]   (make image-test)
set -euo pipefail
image="${1:-viper-machine:test}"
t=$(mktemp -d); export t; name="viper-machine-test-$$"
trap 'docker rm -f "$name" "$name-2" >/dev/null 2>&1 || true; rm -rf "$t"' EXIT
ok=0; bad=0
check() { # check "what" command...
  local what="$1"; shift
  if "$@" >"$t/out" 2>&1; then echo "ok    $what"; ok=$((ok+1)); else echo "FAIL  $what"; sed 's/^/        /' "$t/out" | tail -n 5; bad=$((bad+1)); fi
}

mkdir -p "$t/secret" "$t/home" && chmod 777 "$t/home"
ssh-keygen -q -t ed25519 -N '' -f "$t/ca" -C testca
ssh-keygen -q -t ed25519 -N '' -f "$t/host_key" -C host
ssh-keygen -q -t ed25519 -N '' -f "$t/user" -C user
ssh-keygen -q -s "$t/ca" -I vmtest -h -n vmtest "$t/host_key.pub"
cp "$t/host_key" "$t/host_key-cert.pub" "$t/secret/"; cp "$t/user.pub" "$t/secret/authorized_keys"; chmod -R a+rX "$t/secret"
printf '@cert-authority * %s\n' "$(cut -d' ' -f1,2 "$t/ca.pub")" > "$t/known_hosts"

run() { # run NAME PORT HOMEDIR
  docker run -d --name "$1" --user 1000:1000 --cap-drop ALL --security-opt no-new-privileges \
    -p "127.0.0.1:$2:2222" -v "$t/secret:/run/viper/secret:ro" ${3:+-v "$3:/home/builder"} "$image" >/dev/null
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
cfg=$(docker exec "$name" sshd -T -f /opt/viper/sshd_config)
for want in "passwordauthentication no" "kbdinteractiveauthentication no" "pubkeyauthentication yes" "permitrootlogin no" "permitemptypasswords no" "authenticationmethods publickey" "port 2222"; do
  check "effective sshd setting: $want" grep -qx "$want" <<<"$cfg"
done
check "the container runs as uid 1000" bash -c "[ \"\$(docker exec $name id -u)\" = 1000 ]"
check "sudo is not available" bash -c "! docker exec $name sudo -n true"
for tool in git gh make gcc tmux jq docker brew claude; do
  check "tool: $tool" sshc 2222 "bash -lc 'command -v $tool'"
done
check "docker client and plugins" sshc 2222 "bash -lc 'docker --version && docker buildx version && docker compose version'"
check "home was seeded: tmux and VS Code settings" sshc 2222 'test -f ~/.tmux.conf && test -f ~/.vscode-server/data/Machine/settings.json'
check "the work directory is writable" sshc 2222 'touch /Development/x && rm /Development/x'
check "brew installs a package without root" sshc 2222 "bash -lc 'HOMEBREW_NO_AUTO_UPDATE=1 brew install hello >/dev/null 2>&1 && hello | head -n 1'"
docker rm -f "$name" >/dev/null

# 2: a volume over the home directory (world-writable and empty, as a cluster makes it): the entrypoint brings the files
run "$name-2" 2223 "$t/home"
check "starts with an empty volume over the home directory" wait_ssh 2223
check "an empty volume is seeded" sshc 2223 'test -f ~/.tmux.conf && test -x ~/.local/bin/claude && test -f ~/.viper-seeded'
check "claude runs from the seeded home" sshc 2223 "bash -lc 'claude --version'"
check "a change in the volume survives a restart" bash -c "$(declare -f sshc); sshc 2223 'echo mine > ~/keep.txt' && docker restart $name-2 >/dev/null && $(declare -f wait_ssh) && wait_ssh 2223 && sshc 2223 'cat ~/keep.txt' | grep -qx mine"

echo "--- $ok ok, $bad failed"
[ "$bad" -eq 0 ]
