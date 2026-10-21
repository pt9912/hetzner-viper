# shellcheck shell=bash
# check.sh / make check: read-only checks of a running machine.

up_web() {
  run setup.sh >/dev/null
  git init -q --bare "$T/origin.git"
  printf 'REPO_URL=%s\nVIPER_CLAUDE_TOKEN=sk-ant-oat01-x\nVIPER_GH_TOKEN=ghp_x\n' "$T/origin.git" > "$W/machines/web.env.local"
  run start.sh web >/dev/null
  : > "$CALLS"
}

test_check_ok_on_a_healthy_machine() {
  up_web
  run check.sh web >"$T/out"
  check "summary" has '^Check OK' "$T/out"
  check "dns cloudflare" has 'ok    Cloudflare 1.1.1.1 resolves to 203\.0\.113\.' "$T/out"
  check "dns google" has 'ok    Google 8.8.8.8 resolves to' "$T/out"
  check "access like another device" has 'ok    connection to claude-web\.test\.invalid verified by the host CA' "$T/out"
  check "certificate validity shown" has 'info  host certificate from .* to ' "$T/out"
  check "claude" has 'ok    Claude Code 2\.1\.306 \(Claude Code\): logged in via VIPER_CLAUDE_TOKEN and answers' "$T/out"
  check "gh" has 'ok    gh version 2\.46\.0 .*: logged in' "$T/out"
  check "repo" has 'ok    repository .*/origin at ' "$T/out"
  check "sshd" has 'ok    sshd: public key only' "$T/out"
  check "read-only" bash -c "! grep -qE '^hcloud .*(create|delete|add-rule)|^curl .*-X (POST|PUT|DELETE)' '$CALLS'"
}

test_check_uses_host_ca_and_dns_name_like_another_device() {
  up_web
  run check.sh web >/dev/null
  check "by name, CA only, no pinned key" has "^ssh -n -F /dev/null .*UserKnownHostsFile=$HOME/.ssh/known_hosts_hetzner_claude_ca .*builder@claude-web\.test\.invalid true$" "$CALLS"
}

test_check_reports_dns_not_resolving() {
  up_web
  export MOCK_DNS_STALE=1
  fails run check.sh web >"$T/out"
  check "message" has "FAIL  Cloudflare 1.1.1.1 resolves to 'nothing', expected 203" "$T/out"
}

test_check_reports_ca_rejection() {
  up_web
  export MOCK_CA_FAIL=1
  fails run check.sh web >"$T/out"
  check "message" has 'FAIL  connection to claude-web\.test\.invalid with the host CA failed: Host key verification failed' "$T/out"
}

test_check_reports_missing_claude_token_on_server() {
  up_web
  rm "$MOCK_STATE/remote-home/.config/viper/claude.env"
  fails run check.sh web >"$T/out"
  check "message" has 'FAIL  Claude Code .*: token not loaded in shells' "$T/out"
}

test_check_reports_weak_sshd_and_cloud_init_errors() {
  up_web
  export MOCK_SSHD_WEAK=1 MOCK_CLOUDINIT_RC=1
  fails run check.sh web >"$T/out"
  check "sshd" has 'FAIL  sshd hardening not in effect' "$T/out"
  check "cloud-init" has 'WARN  cloud-init: error' "$T/out"
}

test_check_without_tokens_only_informs() {
  run setup.sh >/dev/null
  run start.sh web >/dev/null
  run check.sh web >"$T/out"
  check "claude info" has 'info  Claude Code .*: no VIPER_CLAUDE_TOKEN' "$T/out"
  check "gh info" has 'info  gh version .*: not logged in \(no VIPER_GH_TOKEN\)' "$T/out"
  check "summary" has '^Check OK' "$T/out"
}

test_check_of_absent_machine() {
  run setup.sh >/dev/null
  fails run check.sh ghost >/dev/null 2>"$T/err"
  check "message" has 'Server claude-ghost does not exist' "$T/err"
}

test_check_explains_stale_negative_answer_of_local_resolver() {
  up_web
  run check.sh web >"$T/out"   # the sandbox resolver does not know test.invalid
  check "resolver named" has "info  (this computer's|the container's) resolver \\(" "$T/out"
  check "negative caching explained" has "remembers 'no such name' .* for up to 1800 s" "$T/out"
  check "public resolvers still decide" has '^Check OK' "$T/out"
}

test_check_accepts_cloud_init_disabled_after_first_boot() {
  up_web
  export MOCK_CLOUDINIT_DISABLED=1
  run check.sh web >"$T/out"
  check "ok" has 'ok    cloud-init: done \(disabled after the first boot\)' "$T/out"
}
