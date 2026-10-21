# shellcheck shell=bash
# Tests for setup.sh / start.sh. Sourced by run.sh; helpers: run, check, has, count_is.

test_setup_creates_keys_and_ca_trust() {
  run setup.sh >/dev/null
  check "login key"  test -f "$HOME/.ssh/hetzner-build.pub"
  check "host CA"    test -f "$HOME/.ssh/hetzner_claude_host_ca"
  check "wildcard CA trust" has '^@cert-authority \*\.test\.invalid ssh-ed25519 ' "$HOME/.ssh/known_hosts_hetzner_claude_ca"
}

test_start_rejects_bad_machine_names() {
  run setup.sh >/dev/null
  ! run start.sh 2>/dev/null
  ! run start.sh Bad_Name 2>/dev/null
  ! run start.sh -x 2>/dev/null
  check "nothing created" test ! -s <(grep '^hcloud' "$CALLS")
}

test_start_creates_all_resources_for_a_machine() {
  run setup.sh >/dev/null
  run start.sh web >/dev/null
  check "server"   has 'hcloud server create --name claude-web ' "$CALLS"
  check "no extra volume" bash -c "! grep -q -i volume '$CALLS'"
  check "firewall" has 'hcloud firewall create --name claude-ssh' "$CALLS"
  check "dns A record" has '"name": "web.test.invalid"' "$MOCK_STATE/cf.json"
  check "managed marker" has 'managed by hetzner-claude' "$MOCK_STATE/cf.json"
  check "ssh config" has 'HostName web.test.invalid' "$HOME/.ssh/config_hetzner_claude_web"
  check "ssh include" has 'Include .*config_hetzner_claude_web' "$HOME/.ssh/config"
  check "workspace dir created for builder" has "install -d -o builder -g builder '$T/mnt'" "$CALLS"
}

test_user_data_carries_signed_host_key_via_ssh_keys() {
  run setup.sh >/dev/null
  run start.sh web >/dev/null
  ud="$MOCK_STATE/hcloud/server/claude-web.user-data"
  python3 -I - "$ud" "$T" <<'PY'
import sys, yaml
d = yaml.safe_load(open(sys.argv[1]))
assert 'ssh_keys' in d and set(d['ssh_keys']) == {'ed25519_private', 'ed25519_public', 'ed25519_certificate'}
assert d['users'][0]['name'] == 'builder' and len(d['users']) == 1
assert d['users'][0]['ssh_authorized_keys'][0].startswith('ssh-ed25519 ')
assert not any(w['path'].startswith('/etc/ssh/ssh_host_') for w in d['write_files'])
open(sys.argv[2] + '/cert.pub', 'w').write(d['ssh_keys']['ed25519_certificate'] + '\n')
PY
  ssh-keygen -L -f "$T/cert.pub" > "$T/cert.txt"
  check "host cert type" has 'host certificate' "$T/cert.txt"
  check "principal is machine DNS name" has 'web\.test\.invalid' "$T/cert.txt"
  check "signed by our CA" hasf "$(ssh-keygen -lf "$HOME/.ssh/hetzner_claude_host_ca.pub" | awk '{print $2}')" "$T/cert.txt"
}

test_second_machine_shares_key_and_firewall() {
  run setup.sh >/dev/null
  run start.sh web >/dev/null
  run start.sh api >/dev/null
  check "one firewall"  count_is 'hcloud firewall create' "$CALLS" 1
  check "one login key" count_is 'hcloud ssh-key create' "$CALLS" 1
  check "two servers"   count_is 'hcloud server create' "$CALLS" 2
  check "api dns" has '"name": "api.test.invalid"' "$MOCK_STATE/cf.json"
  check "web dns kept" has '"name": "web.test.invalid"' "$MOCK_STATE/cf.json"
  check "own ssh config" test -f "$HOME/.ssh/config_hetzner_claude_api"
}

test_restart_of_running_machine_is_idempotent() {
  run setup.sh >/dev/null
  run start.sh web >/dev/null
  run start.sh web >/dev/null
  check "server created once" count_is 'hcloud server create' "$CALLS" 1
  check "one dns record" test "$(jq length "$MOCK_STATE/cf.json")" = 1
}

test_start_clones_repo_once() {
  run setup.sh >/dev/null
  git init -q --bare "$T/origin.git"
  git clone -q "$T/origin.git" "$T/seed" 2>/dev/null
  (cd "$T/seed" && git -c user.email=t@t -c user.name=t commit -q --allow-empty -m init && git push -q origin HEAD:main 2>/dev/null)
  run start.sh web "$T/origin.git" >/dev/null
  check "cloned into mount point" test -d "$T/mnt/origin/.git"
  check "vscode opens clone" has "code --remote ssh-remote\+hetzner-claude-web $T/mnt/origin" "$CALLS"
  out=$(run start.sh web "$T/origin.git")
  check "second start does not reclone" bash -c "grep -q 'already present' <<<'$out'"
}

test_repo_from_machine_file_with_deploy_key() {
  run setup.sh >/dev/null
  git init -q --bare "$T/origin.git"
  echo "key-material" > "$T/deploy"
  printf 'REPO_URL=%s\nGIT_DEPLOY_KEY_FILE=%s\n' "$T/origin.git" "$T/deploy" > "$W/machines/web.env.local"
  run start.sh web >/dev/null
  check "key copied" test "$(cat "$MOCK_STATE/scp/deploy_key")" = key-material
  check "repo dir" test -d "$T/mnt/origin/.git"
  check "core.sshCommand set" has 'deploy_key' "$T/mnt/origin/.git/config"
}

test_machine_file_overrides_server_type() {
  run setup.sh >/dev/null
  echo 'SERVER_TYPE=cx11' > "$W/machines/small.env.local"
  run start.sh small >/dev/null
  check "override" has '--type cx11' "$CALLS"
  run start.sh big >/dev/null
  check "default elsewhere" has 'claude-big .*--type cx53' "$CALLS"
}

test_start_without_setup_fails_cleanly() {
  ! run start.sh web 2>"$T/err"
  check "hint" has 'Run ./setup.sh first' "$T/err"
}

test_dns_not_propagated_keeps_server() {
  run setup.sh >/dev/null
  MOCK_DNS_STALE=1 && export MOCK_DNS_STALE
  ! run start.sh web 2>"$T/err"
  check "message" has 'DNS not yet propagated' "$T/err"
  check "server kept" test -f "$MOCK_STATE/hcloud/server/claude-web.json"
}

test_dns_refuses_foreign_record() {
  run setup.sh >/dev/null
  echo '[{"id":"x1","type":"A","name":"web.test.invalid","content":"198.51.100.1","comment":"mine"}]' > "$MOCK_STATE/cf.json"
  ! MACHINE=web run dns.sh set 203.0.113.9 2>"$T/err"
  check "refused" has 'not managed by this setup' "$T/err"
  check "record untouched" has '198.51.100.1' "$MOCK_STATE/cf.json"
}

test_ready_when_cloud_init_done_with_warnings() {
  run setup.sh >/dev/null
  export MOCK_CLOUDINIT_RC=2
  run start.sh web >/dev/null
}

test_not_ready_when_cloud_init_failed() {
  run setup.sh >/dev/null
  export MOCK_CLOUDINIT_RC=1
  ! run start.sh web >/dev/null 2>"$T/err"
  check "message" has 'not ready' "$T/err"
}

test_not_ready_without_claude_marker() {
  run setup.sh >/dev/null
  rm "$MOCK_STATE/claude-ready"
  ! run start.sh web >/dev/null 2>"$T/err"
  check "message" has 'not ready' "$T/err"
}

test_cloud_init_marks_ready_only_after_successful_install() {
  run setup.sh >/dev/null
  run start.sh web >/dev/null
  python3 -I - "$MOCK_STATE/hcloud/server/claude-web.user-data" <<'PY'
import sys, yaml
d = yaml.safe_load(open(sys.argv[1]))
install = [c for c in d['runcmd'] if 'install.sh' in c]
assert len(install) == 1 and 'pipefail' in install[0] and install[0].endswith('&& touch /var/tmp/claude-ready'), install
assert not any(c == 'touch /var/tmp/claude-ready' for c in d['runcmd'])
assert not any('60-host-cert' in w['path'] for w in d['write_files'])
assert 'systemctl try-reload-or-restart ssh' in d['runcmd']
PY
}

test_firewall_without_rules_gets_ssh_rule() {
  run setup.sh >/dev/null
  hcloud firewall create --name claude-ssh >/dev/null
  run start.sh web >/dev/null
  check "rule added" count_is 'hcloud firewall add-rule claude-ssh' "$CALLS" 1
  run start.sh api >/dev/null
  check "no second rule" count_is 'hcloud firewall add-rule claude-ssh' "$CALLS" 1
}

test_missing_deploy_key_fails_before_creating_anything() {
  run setup.sh >/dev/null
  printf 'REPO_URL=git@example.org:o/r.git\nGIT_DEPLOY_KEY_FILE=%s\n' "$T/nope" > "$W/machines/web.env.local"
  ! run start.sh web >/dev/null 2>"$T/err"
  check "message" has 'File not readable' "$T/err"
  check "no server" test ! -f "$MOCK_STATE/hcloud/server/claude-web.json"
}

test_scripts_use_own_ssh_config_only() {
  run setup.sh >/dev/null
  git init -q --bare "$T/origin.git"
  echo k > "$T/deploy"
  printf 'REPO_URL=%s\nGIT_DEPLOY_KEY_FILE=%s\n' "$T/origin.git" "$T/deploy" > "$W/machines/web.env.local"
  run start.sh web >/dev/null
  echo DELETE | run stop.sh web >/dev/null 2>&1 || true
  check "every ssh/scp call passes -F" bash -c "! grep -E '^(ssh|scp) ' '$CALLS' | grep -v -- '-F $HOME/.ssh/config_hetzner_claude_web'"
}

test_gh_installed_by_cloud_init() {
  run setup.sh >/dev/null
  run start.sh web >/dev/null
  python3 -I -c "import sys,yaml; assert 'gh' in yaml.safe_load(open(sys.argv[1]))['packages']" "$MOCK_STATE/hcloud/server/claude-web.user-data"
}

test_gh_token_passed_via_stdin_before_clone() {
  run setup.sh >/dev/null
  git init -q --bare "$T/origin.git"
  export GH_TOKEN=ghp_secret123
  run start.sh web "$T/origin.git" >/dev/null
  check "token reached gh" test "$(cat "$MOCK_STATE/gh-token")" = ghp_secret123
  check "git uses gh credentials" has 'gh auth setup-git' "$CALLS"
  check "token not in any command line" bash -c "! grep -q ghp_secret123 '$CALLS'"
  check "token not in user-data" bash -c "! grep -q ghp_secret123 '$MOCK_STATE/hcloud/server/claude-web.user-data'"
  check "login before clone" bash -c "grep -n -E 'gh auth login|bash -s -- $T/origin.git' '$CALLS' | head -1 | grep -q 'gh auth login'"
}

test_gh_token_per_machine() {
  run setup.sh >/dev/null
  echo 'GH_TOKEN=ghp_web_only' > "$W/machines/web.env.local"
  run start.sh web >/dev/null
  check "machine token used" test "$(cat "$MOCK_STATE/gh-token")" = ghp_web_only
  rm "$MOCK_STATE/gh-token"
  run start.sh api >/dev/null
  check "other machine gets no token" test ! -f "$MOCK_STATE/gh-token"
}

test_no_gh_login_without_token() {
  run setup.sh >/dev/null
  run start.sh web >/dev/null
  check "no gh call" bash -c "! grep -q 'gh auth' '$CALLS'"
}
