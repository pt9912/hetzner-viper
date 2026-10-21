# shellcheck shell=bash
# Trust logic against a real OpenSSH pair: the ssh config is written by the same
# write_ssh_config the scripts use, sshd runs unprivileged on 127.0.0.1. Needs openssh-server
# (present in the test image, `make docker-test`); skipped elsewhere.

have_real_sshd() { [[ -x /usr/sbin/sshd && -x /usr/bin/ssh && -d /run/sshd ]]; }

# real_sshd <validity>: host key signed by our CA for claude-web.<domain>, valid for <validity>
real_sshd() {
  local d="$T/sshd"
  [[ ! -f "$d/pid" ]] || { kill "$(cat "$d/pid")" 2>/dev/null; sleep 0.3; }
  mkdir -p "$d"; rm -f "$d"/hk*
  ssh-keygen -q -t ed25519 -N '' -C claude-web -f "$d/hk"
  ssh-keygen -q -s "$HOME/.ssh/hetzner_claude_host_ca" -I t -h -n claude-web.test.invalid -V "$1" "$d/hk.pub"
  cp "$HOME/.ssh/hetzner-build.pub" "$d/authorized_keys"
  cat > "$d/sshd_config" <<CFG
Port 2222
ListenAddress 127.0.0.1
HostKey $d/hk
HostCertificate $d/hk-cert.pub
PidFile $d/pid
UsePAM no
StrictModes no
AuthorizedKeysFile $d/authorized_keys
CFG
  # no inherited stdout/stderr/fd 3: a still running sshd must not keep the harness's
  # output pipe open when a check fails (run.sh kills it after the test)
  /usr/sbin/sshd -D -e -f "$d/sshd_config" </dev/null >/dev/null 2>"$d/log" 3>&- &
  for _ in 1 2 3 4 5 6 7 8 9 10; do [[ -f "$d/pid" ]] && break; sleep 0.2; done
}

# connect via the scripts' own config (user/port overridden on the command line)
real_ssh() {
  /usr/bin/ssh -n -F "$HOME/.ssh/config_hetzner_claude-web" -l "$(id -un)" -p 2222 -o BatchMode=yes hetzner-claude-web true 2>"$T/ssh-err"
}

# write the config exactly like start.sh/stop.sh would (via common.sh)
scripts_config() {
  (cd "$W" && MACHINE=web bash -c 'source ./common.sh; write_ssh_config 127.0.0.1')
}

pin_key() { echo "claude-web.test.invalid $(cut -d' ' -f1,2 "$1")" > "$HOME/.ssh/known_hosts_hetzner_claude-web"; }

kind_of_last_error() {
  (cd "$W" && MACHINE=web bash -c 'source ./common.sh; ssh_failure_kind "$(cat "$1")"' _ "$T/ssh-err")
}

test_real_ssh_trust_logic() {
  have_real_sshd || { echo "    skipped: no real sshd (runs in make docker-test)" >&3; return 0; }
  run setup.sh >/dev/null

  real_sshd -5m:+7d; pin_key "$T/sshd/hk.pub"; scripts_config
  check "pinned key + valid cert" real_ssh

  real_sshd 20200101:20200102; pin_key "$T/sshd/hk.pub"; scripts_config
  check "pinned key + expired cert" real_ssh

  ssh-keygen -q -t ed25519 -N '' -f "$T/old"
  real_sshd -5m:+7d; pin_key "$T/old.pub"; scripts_config
  fails real_ssh
  check "stale pin rejected despite valid CA cert" test "$(kind_of_last_error)" = hostkey

  real_sshd -5m:+7d; rm -f "$HOME/.ssh/known_hosts_hetzner_claude-web"; scripts_config
  check "no pin: CA fallback with valid cert" real_ssh

  real_sshd 20200101:20200102; scripts_config
  fails real_ssh
  check "no pin + expired cert rejected as host key problem" test "$(kind_of_last_error)" = hostkey

  kill "$(cat "$T/sshd/pid")"; sleep 0.3
  fails real_ssh
  check "nothing listening is a network problem" test "$(kind_of_last_error)" = network
}
