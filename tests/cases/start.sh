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
  fails run start.sh 2>/dev/null
  fails run start.sh Bad_Name 2>/dev/null
  fails run start.sh -x 2>/dev/null
  check "nothing created" bash -c "! grep -q '^hcloud' '$CALLS'"
}

test_start_creates_all_resources_for_a_machine() {
  run setup.sh >/dev/null
  run start.sh web >/dev/null
  check "server"   has 'hcloud server create --name claude-web ' "$CALLS"
  check "no extra volume" bash -c "! grep -q -i volume '$CALLS'"
  check "firewall" has 'hcloud firewall create --name claude-ssh' "$CALLS"
  check "dns A record" has '"name": "claude-web.test.invalid"' "$MOCK_STATE/cf.json"
  check "managed marker" has 'managed by hetzner-claude' "$MOCK_STATE/cf.json"
  check "ssh by IP" has 'HostName 203\.0\.113\.' "$HOME/.ssh/config_hetzner_claude-web"
  check "host key checked for DNS name" has 'HostKeyAlias claude-web\.test\.invalid' "$HOME/.ssh/config_hetzner_claude-web"
  check "ssh include" has 'Include .*config_hetzner_claude-web' "$HOME/.ssh/config"
  check "workspace dir created for builder" has "install -d -o builder -g builder $T/mnt" "$CALLS"
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
  check "principal is machine DNS name" has 'claude-web\.test\.invalid' "$T/cert.txt"
  check "signed by our CA" hasf "$(ssh-keygen -lf "$HOME/.ssh/hetzner_claude_host_ca.pub" | awk '{print $2}')" "$T/cert.txt"
}

test_second_machine_shares_key_and_firewall() {
  run setup.sh >/dev/null
  run start.sh web >/dev/null
  run start.sh api >/dev/null
  check "one firewall"  count_is 'hcloud firewall create' "$CALLS" 1
  check "one login key" count_is 'hcloud ssh-key create' "$CALLS" 1
  check "two servers"   count_is 'hcloud server create' "$CALLS" 2
  check "api dns" has '"name": "claude-api.test.invalid"' "$MOCK_STATE/cf.json"
  check "web dns kept" has '"name": "claude-web.test.invalid"' "$MOCK_STATE/cf.json"
  check "own ssh config" test -f "$HOME/.ssh/config_hetzner_claude-api"
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
  echo 'SERVER_TYPE=cpx02' > "$W/machines/small.env.local"
  run start.sh small >/dev/null
  check "override" has '--type cpx02' "$CALLS"
  run start.sh big >/dev/null
  check "default elsewhere" has 'claude-big .*--type cpx32' "$CALLS"
}

test_start_without_setup_fails_cleanly() {
  fails run start.sh web 2>"$T/err"
  check "hint" has 'Run ./setup.sh first' "$T/err"
}


test_dns_refuses_foreign_record() {
  run setup.sh >/dev/null
  echo '[{"id":"x1","type":"A","name":"claude-web.test.invalid","content":"198.51.100.1","comment":"mine"}]' > "$MOCK_STATE/cf.json"
  MACHINE=web fails run dns.sh set 203.0.113.9 2>"$T/err"
  check "refused" has 'not managed by this setup' "$T/err"
  check "record untouched" has '198.51.100.1' "$MOCK_STATE/cf.json"
}

test_ready_when_cloud_init_done_with_warnings() {
  run setup.sh >/dev/null
  export MOCK_CLOUDINIT_RC=2
  run start.sh web >/dev/null
}

test_cloud_init_errors_only_warn_when_everything_is_installed() {
  run setup.sh >/dev/null
  export MOCK_CLOUDINIT_RC=1
  run start.sh web >/dev/null 2>"$T/err"
  check "warning" has 'cloud-init reported errors' "$T/err"
}

test_docker_not_running_fails() {
  run setup.sh >/dev/null
  export MOCK_DOCKER_FAIL=1
  fails run start.sh web >/dev/null 2>"$T/err"
  check "message" has 'Docker is not running' "$T/err"
}

test_ssh_never_reachable_fails() {
  run setup.sh >/dev/null
  export MOCK_SSH_DOWN=1
  fails run start.sh web >/dev/null 2>"$T/err"
  check "message" has 'SSH not reachable' "$T/err"
  check "server kept" test -f "$MOCK_STATE/hcloud/server/claude-web.json"
}

test_missing_claude_install_is_repaired_by_start() {
  run setup.sh >/dev/null
  rm "$MOCK_STATE/claude-ready"
  run start.sh web >/dev/null
  check "installer run over ssh" has 'install\.sh' "$CALLS"
  check "marker now present" test -f "$MOCK_STATE/claude-ready"
}

test_failed_claude_install_reported() {
  run setup.sh >/dev/null
  rm "$MOCK_STATE/claude-ready"
  export MOCK_INSTALL_FAIL=1
  fails run start.sh web >/dev/null 2>"$T/err"
  check "message" has 'installation failed' "$T/err"
}

test_claude_install_is_retried_when_rate_limited() {
  run setup.sh >/dev/null
  rm "$MOCK_STATE/claude-ready"
  MOCK_INSTALL_FAIL_TIMES=2 run start.sh web >"$T/out" 2>"$T/err"
  check "retried" has 'installation failed \(attempt 1 of 5\); retrying in 30 s' "$T/err"
  check "second retry" has 'attempt 2 of 5\); retrying in 60 s' "$T/err"
  check "third attempt worked" test -f "$MOCK_STATE/claude-ready"
  check "machine ready" has '^Ready: ssh hetzner-claude-web' "$T/out"
  rm "$MOCK_STATE/claude-ready" "$MOCK_STATE/install-attempts"
  MOCK_INSTALL_FAIL_TIMES=9 fails run start.sh web >/dev/null 2>"$T/err2"
  check "gives up after 5" test "$(cat "$MOCK_STATE/install-attempts")" = 5
  check "named" has 'rate limit, HTTP 429' "$T/err2"
}

test_cloud_init_marks_ready_only_after_successful_install() {
  run setup.sh >/dev/null
  run start.sh web >/dev/null
  python3 -I - "$MOCK_STATE/hcloud/server/claude-web.user-data" <<'PY'
import sys, yaml
d = yaml.safe_load(open(sys.argv[1]))
install = [c for c in d['runcmd'] if 'install.sh' in c]
assert len(install) == 1 and 'pipefail' in install[0] and '&& touch /var/tmp/claude-ready && break' in install[0], install
# rate limit (HTTP 429): retried with pauses, the marker only after a success
assert install[0].startswith('for i in') and '[ $i -lt 5 ] && sleep' in install[0], install
assert not any(c == 'touch /var/tmp/claude-ready' for c in d['runcmd'])
assert not any('60-host-cert' in w['path'] for w in d['write_files'])
assert 'systemctl try-reload-or-restart ssh' in d['runcmd']
PY
}

test_firewall_without_rules_gets_ssh_and_icmp_rules() {
  run setup.sh >/dev/null
  hcloud firewall create --name claude-ssh >/dev/null
  run start.sh web >/dev/null
  check "ssh rule" count_is 'hcloud firewall add-rule claude-ssh --direction in --protocol tcp --port 22' "$CALLS" 1
  check "icmp rule" count_is 'hcloud firewall add-rule claude-ssh --direction in --protocol icmp' "$CALLS" 1
  run start.sh api >/dev/null
  check "no further rules" count_is 'hcloud firewall add-rule' "$CALLS" 2
}

test_existing_firewall_with_only_ssh_gets_icmp() {
  run setup.sh >/dev/null
  hcloud firewall create --name claude-ssh >/dev/null
  hcloud firewall add-rule claude-ssh --direction in --protocol tcp --port 22 --source-ips 10.0.0.0/8 >/dev/null
  : > "$CALLS"
  echo 'SSH_ALLOWED_CIDRS=10.0.0.0/8' >> "$W/.env.local"
  run start.sh web >/dev/null
  check "icmp added with the same networks" has 'add-rule claude-ssh --direction in --protocol icmp --source-ips 10\.0\.0\.0/8' "$CALLS"
  check "ssh rule not duplicated" bash -c "! grep -q 'protocol tcp' '$CALLS'"
}

test_missing_deploy_key_fails_before_creating_anything() {
  run setup.sh >/dev/null
  printf 'REPO_URL=git@example.org:o/r.git\nGIT_DEPLOY_KEY_FILE=%s\n' "$T/nope" > "$W/machines/web.env.local"
  fails run start.sh web >/dev/null 2>"$T/err"
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
  check "every ssh/scp call passes -F" bash -c "! grep -E '^(ssh|scp) ' '$CALLS' | grep -v -- '-F $HOME/.ssh/config_hetzner_claude-web'"
}

test_gh_installed_by_cloud_init() {
  run setup.sh >/dev/null
  run start.sh web >/dev/null
  python3 -I -c "import sys,yaml; assert 'gh' in yaml.safe_load(open(sys.argv[1]))['packages']" "$MOCK_STATE/hcloud/server/claude-web.user-data"
}

test_gh_token_passed_via_stdin_before_clone() {
  run setup.sh >/dev/null
  git init -q --bare "$T/origin.git"
  export VIPER_GH_TOKEN=ghp_secret123
  run start.sh web "$T/origin.git" >/dev/null
  check "token reached gh" test "$(cat "$MOCK_STATE/gh-token")" = ghp_secret123
  check "git uses gh credentials" has 'gh auth setup-git' "$CALLS"
  check "token not in any command line" bash -c "! grep -q ghp_secret123 '$CALLS'"
  check "token not in user-data" bash -c "! grep -q ghp_secret123 '$MOCK_STATE/hcloud/server/claude-web.user-data'"
  check "login before clone" bash -c "grep -n -E 'gh auth login|bash -s -- $T/origin.git' '$CALLS' | head -1 | grep -q 'gh auth login'"
}

test_gh_token_per_machine() {
  run setup.sh >/dev/null
  echo 'VIPER_GH_TOKEN=ghp_web_only' > "$W/machines/web.env.local"
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

test_clone_without_branch_and_with_odd_path() {
  run setup.sh >/dev/null
  git init -q --bare "$T/my repo;x.git"
  run start.sh web "$T/my repo;x.git" >/dev/null
  check "cloned despite empty branch arg and metacharacters" test -d "$T/mnt/my repo;x/.git"
}

test_clone_with_deploy_key_and_no_branch() {
  run setup.sh >/dev/null
  git init -q --bare "$T/origin.git"; echo k > "$T/deploy"
  printf 'REPO_URL=%s\nGIT_DEPLOY_KEY_FILE=%s\n' "$T/origin.git" "$T/deploy" > "$W/machines/web.env.local"
  run start.sh web >/dev/null
  check "cloned" test -d "$T/mnt/origin/.git"
}

test_scripts_trust_only_the_pinned_host_key() {
  run setup.sh >/dev/null
  run start.sh web >/dev/null
  pub=$(python3 -I -c "import sys,yaml; print(yaml.safe_load(open(sys.argv[1]))['ssh_keys']['ed25519_public'])" "$MOCK_STATE/hcloud/server/claude-web.user-data" | cut -d' ' -f1,2)
  check "pinned key = server key, for DNS name" test "$(cat "$HOME/.ssh/known_hosts_hetzner_claude-web")" = "claude-web.test.invalid $pub"
  check "only the pin is trusted" has "UserKnownHostsFile $HOME/.ssh/known_hosts_hetzner_claude-web\$" "$HOME/.ssh/config_hetzner_claude-web"
  check "plain key allowed (cert may expire)" has 'HostKeyAlgorithms ssh-ed25519-cert-v01@openssh.com,ssh-ed25519' "$HOME/.ssh/config_hetzner_claude-web"
}

test_without_pin_the_host_ca_is_trusted() {
  run setup.sh >/dev/null
  run start.sh web >/dev/null
  rm "$HOME/.ssh/known_hosts_hetzner_claude-web"   # e.g. server created on another computer
  run start.sh web >/dev/null
  check "CA file used" has "UserKnownHostsFile $HOME/.ssh/known_hosts_hetzner_claude_ca\$" "$HOME/.ssh/config_hetzner_claude-web"
}

test_existing_public_key_is_reused_under_its_name() {
  run setup.sh >/dev/null
  hcloud ssh-key create --name my-laptop --public-key-from-file "$HOME/.ssh/hetzner-build.pub" >/dev/null
  run start.sh web >/dev/null
  check "no new key" count_is 'hcloud ssh-key create' "$CALLS" 1
  check "server uses existing key" has '--ssh-key my-laptop' "$CALLS"
}

test_key_name_taken_by_other_public_key_fails() {
  run setup.sh >/dev/null
  ssh-keygen -q -t ed25519 -N '' -f "$T/other"
  hcloud ssh-key create --name claude-login --public-key-from-file "$T/other.pub" >/dev/null
  fails run start.sh web >/dev/null 2>"$T/err"
  check "message" has 'belongs to a different public key' "$T/err"
}

test_firewall_describe_error_aborts() {
  run setup.sh >/dev/null
  export MOCK_DESCRIBE_FAIL=1
  fails run start.sh web >/dev/null 2>&1
  check "no rule-less server" test ! -f "$MOCK_STATE/hcloud/server/claude-web.json"
}

test_same_machine_name_under_two_prefixes_does_not_collide() {
  run setup.sh >/dev/null
  run start.sh web >/dev/null
  echo 'SERVER_PREFIX=alice' > "$W/machines/web.env.local"
  run start.sh web >/dev/null
  check "two dns records" test "$(jq length "$MOCK_STATE/cf.json")" = 2
  check "own config" test -f "$HOME/.ssh/config_hetzner_alice-web"
  check "first config untouched" has 'claude-web\.test\.invalid' "$HOME/.ssh/config_hetzner_claude-web"
}

test_hcloud_error_aborts_start_without_creating() {
  run setup.sh >/dev/null
  export MOCK_HCLOUD_FAIL=1
  fails run start.sh web >/dev/null 2>"$T/err"
  check "message" has 'Hetzner API not usable' "$T/err"
  check "no dns" test ! -f "$MOCK_STATE/cf.json"
}

test_cloudflare_token_not_in_argv() {
  run setup.sh >/dev/null
  export CLOUDFLARE_API_TOKEN=cf_secret_42
  run start.sh web >/dev/null
  check "dns written" has 'claude-web\.test\.invalid' "$MOCK_STATE/cf.json"
  check "token not on any command line" bash -c "! grep -q cf_secret_42 '$CALLS'"
}

test_vscode_does_not_inherit_tokens() {
  run setup.sh >/dev/null
  export VIPER_GH_TOKEN=ghp_x GH_TOKEN=ghp_local VIPER_CLAUDE_TOKEN=sk-ant-oat01-x
  run start.sh web >/dev/null
  check "code launched" has '^code ' "$CALLS"
  check "no tokens in its environment" test ! -s "$MOCK_STATE/code-env"
}

test_exported_gh_token_is_not_copied_to_the_server() {
  run setup.sh >/dev/null
  export GH_TOKEN=ghp_my_local_token
  run start.sh web >/dev/null
  check "no gh login" bash -c "! grep -q 'gh auth' '$CALLS'"
}

test_host_key_rejection_is_named_not_called_unreachable() {
  run setup.sh >/dev/null
  export MOCK_SSH_HOSTKEY_FAIL=1
  fails run start.sh web >/dev/null 2>"$T/err"
  check "named" has 'host key is not trusted' "$T/err"
  check "not called unreachable" bash -c "! grep -q 'SSH not reachable' '$T/err'"
}

test_stale_pin_dropped_when_server_was_recreated() {
  run setup.sh >/dev/null
  run start.sh web >/dev/null
  old=$(cat "$HOME/.ssh/known_hosts_hetzner_claude-web")
  # another computer deletes and recreates claude-web: new ID, new host key
  cp "$MOCK_STATE/hcloud/server/claude-web.user-data" "$T/ud"
  rm "$MOCK_STATE/hcloud/server/claude-web.json"
  hcloud server create --name claude-web --user-data-from-file "$T/ud" >/dev/null
  echo "ssh-ed25519 AAAAnewkey claude-web" > "$MOCK_STATE/remote-hostkey.pub"
  run start.sh web >"$T/out"
  check "notice" has 'was recreated' "$T/out"
  check "new key pinned" test "$(cat "$HOME/.ssh/known_hosts_hetzner_claude-web")" = "claude-web.test.invalid ssh-ed25519 AAAAnewkey"
  check "differs from old" test "$(cat "$HOME/.ssh/known_hosts_hetzner_claude-web")" != "$old"
}

test_missing_pin_is_recorded_after_ca_verified_connection() {
  run setup.sh >/dev/null
  run start.sh web >/dev/null
  rm "$HOME/.ssh/known_hosts_hetzner_claude-web" "$HOME/.local/state/hetzner-claude/id-claude-web"
  echo "ssh-ed25519 AAAAserverkey root@host" > "$MOCK_STATE/remote-hostkey.pub"
  run start.sh web >/dev/null
  check "pinned" test "$(cat "$HOME/.ssh/known_hosts_hetzner_claude-web")" = "claude-web.test.invalid ssh-ed25519 AAAAserverkey"
  check "config switched to pin" has "UserKnownHostsFile $HOME/.ssh/known_hosts_hetzner_claude-web\$" "$HOME/.ssh/config_hetzner_claude-web"
}

test_symlinked_ssh_config_stays_a_symlink() {
  run setup.sh >/dev/null
  mkdir -p "$T/dotfiles"; echo 'Host other' > "$T/dotfiles/ssh_config"
  ln -sf "$T/dotfiles/ssh_config" "$HOME/.ssh/config"
  run start.sh web >/dev/null
  check "still a symlink" test -L "$HOME/.ssh/config"
  check "include in target" has 'Include .*config_hetzner_claude-web' "$T/dotfiles/ssh_config"
  check "own content kept" has '^Host other' "$T/dotfiles/ssh_config"
}

test_existing_key_name_with_spaces_is_reused() {
  run setup.sh >/dev/null
  hcloud ssh-key create --name 'my laptop key' --public-key-from-file "$HOME/.ssh/hetzner-build.pub" >/dev/null
  run start.sh web >/dev/null
  check "no second key" count_is 'hcloud ssh-key create' "$CALLS" 1
  check "used by name" has '--ssh-key my laptop key' "$CALLS"
}

test_host_key_comment_does_not_leak_local_user() {
  run setup.sh >/dev/null
  run start.sh web >/dev/null
  check "comment is server name" has 'ed25519_public: "ssh-ed25519 [^ ]+ claude-web"' "$MOCK_STATE/hcloud/server/claude-web.user-data"
}

test_ssh_config_has_keepalive() {
  run setup.sh >/dev/null
  run start.sh web >/dev/null
  check "keepalive" has 'ServerAliveInterval 15' "$HOME/.ssh/config_hetzner_claude-web"
}

# a home like Ubuntu's: .bashrc returns early for non-interactive shells
ubuntu_home() {
  local h="$MOCK_STATE/remote-home"
  mkdir -p "$h"
  printf 'case $- in\n    *i*) ;;\n      *) return;;\nesac\nexport FROM_BASHRC=1\n' > "$h/.bashrc"
  printf 'if [ -n "$BASH_VERSION" ] && [ -f "$HOME/.bashrc" ]; then . "$HOME/.bashrc"; fi\n' > "$h/.profile"
}

test_claude_token_reaches_noninteractive_and_login_shells() {
  run setup.sh >/dev/null
  ubuntu_home
  export VIPER_CLAUDE_TOKEN='sk-ant-oat01-a$b"c'"'"'d'
  run start.sh web >/dev/null
  h="$MOCK_STATE/remote-home"
  check "file is private" test "$(stat -c %a "$h/.config/viper/claude.env")" = 600
  check "non-interactive ssh shell" test "$(HOME="$h" bash -c '. ~/.bashrc; printf %s "$CLAUDE_CODE_OAUTH_TOKEN"')" = "$VIPER_CLAUDE_TOKEN"
  check "login shell" test "$(HOME="$h" bash -lc 'printf %s "$CLAUDE_CODE_OAUTH_TOKEN"')" = "$VIPER_CLAUDE_TOKEN"
  check "rest of .bashrc kept" has '^export FROM_BASHRC=1' "$h/.bashrc"
  check "token not on any command line" bash -c "! grep -qF 'sk-ant-oat01' '$CALLS'"
  check "token not in user-data" bash -c "! grep -qF 'sk-ant-oat01' '$MOCK_STATE/hcloud/server/claude-web.user-data'"
}

test_claude_token_rerun_does_not_duplicate_and_updates() {
  run setup.sh >/dev/null
  ubuntu_home
  VIPER_CLAUDE_TOKEN=first run start.sh web >/dev/null
  VIPER_CLAUDE_TOKEN=second run start.sh web >/dev/null
  h="$MOCK_STATE/remote-home"
  check "one loader line in .bashrc" count_is 'viper/claude.env' "$h/.bashrc" 1
  check "one loader line in .profile" count_is 'viper/claude.env' "$h/.profile" 1
  check "new token" test "$(HOME="$h" bash -c '. ~/.bashrc; printf %s "$CLAUDE_CODE_OAUTH_TOKEN"')" = second
}

test_locally_exported_claude_token_is_not_copied() {
  run setup.sh >/dev/null
  export CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oat01-local
  run start.sh web >/dev/null
  check "no login" test ! -e "$MOCK_STATE/remote-home/.config/viper/claude.env"
  check "no tokens for VS Code" test ! -s "$MOCK_STATE/code-env"
}

test_claude_token_works_without_existing_bashrc_and_profile() {
  run setup.sh >/dev/null
  export VIPER_CLAUDE_TOKEN=sk-ant-oat01-x
  run start.sh web >/dev/null
  h="$MOCK_STATE/remote-home"
  check "loader created" has 'viper/claude.env' "$h/.bashrc"
  check "token loaded" test "$(HOME="$h" bash -c '. ~/.bashrc; printf %s "$CLAUDE_CODE_OAUTH_TOKEN"')" = sk-ant-oat01-x
}

test_unwritable_ssh_config_only_warns() {
  run setup.sh >/dev/null
  mkdir -p "$T/dotfiles"; echo 'Host other' > "$T/dotfiles/ssh_config"; chmod 444 "$T/dotfiles/ssh_config"
  ln -sf "$T/dotfiles/ssh_config" "$HOME/.ssh/config"
  run preflight.sh web >"$T/pre"
  check "preflight warns" has 'WARN  ~/.ssh/config .* is not writable' "$T/pre"
  run start.sh web >"$T/out" 2>"$T/err"
  check "start goes on" has '^Ready: ssh hetzner-claude-web' "$T/out"
  check "warning" has "WARNING: could not edit ~/.ssh/config" "$T/err"
  chmod 644 "$T/dotfiles/ssh_config"
}

test_ssh_repo_url_without_deploy_key_fails_preflight() {
  run setup.sh >/dev/null
  fails run start.sh web git@github.com:o/r.git >/dev/null 2>"$T/err"
  check "message" has 'needs GIT_DEPLOY_KEY_FILE' "$T/err"
  check "no server" test ! -f "$MOCK_STATE/hcloud/server/claude-web.json"
}

test_ssh_repo_url_of_gh_host_goes_over_https_with_token() {
  run setup.sh >/dev/null
  export VIPER_GH_TOKEN=ghp_x
  run preflight.sh web git@github.com:o/r.git >"$T/pre"
  check "preflight ok" has 'will be cloned over HTTPS with VIPER_GH_TOKEN' "$T/pre"
  fails run preflight.sh web git@gitlab.com:o/r.git >"$T/pre2"
  check "other host still needs a key" has 'needs GIT_DEPLOY_KEY_FILE' "$T/pre2"
  run start.sh web >/dev/null
  check "git rewrites SSH URLs to HTTPS" has 'insteadOf git@github\.com: .*insteadOf ssh://git@github\.com/' "$CALLS"
}

test_repo_url_starting_with_dash_is_rejected() {
  run setup.sh >/dev/null
  fails run start.sh web '--upload-pack=touch /tmp/x' >/dev/null 2>"$T/err"
  check "message" has "REPO_URL must not start with '-'" "$T/err"
  check "no server" test ! -f "$MOCK_STATE/hcloud/server/claude-web.json"
}

test_ssh_config_never_forwards_agent_or_x11() {
  run setup.sh >/dev/null
  run start.sh web >/dev/null
  check "agent" has '^    ForwardAgent no$' "$HOME/.ssh/config_hetzner_claude-web"
  check "x11" has '^    ForwardX11 no$' "$HOME/.ssh/config_hetzner_claude-web"
}

test_reused_server_with_untrusted_key_fails_fast() {
  run setup.sh >/dev/null
  run start.sh web >/dev/null
  : > "$CALLS"
  MOCK_SSH_HOSTKEY_FAIL=1 fails run start.sh web >/dev/null 2>"$T/err"
  check "message" has 'host key is not trusted' "$T/err"
  check "no long retry" count_is 'cloud-init status' "$CALLS" 1
  : > "$CALLS"
  MOCK_SSH_AUTH_FAIL=1 fails run start.sh web >/dev/null 2>"$T/err"
  check "auth message" has 'rejects the login key' "$T/err"
  check "no long retry (auth)" count_is 'cloud-init status' "$CALLS" 1
}

test_classic_token_with_broader_org_scope_is_accepted() {
  run setup.sh >/dev/null
  export VIPER_GH_TOKEN=ghp_x MOCK_GH_SCOPES='repo, admin:org'
  run preflight.sh web >"$T/out"
  check "no scope failure" bash -c "! grep -q 'lacks scope' '$T/out'"
}

test_new_server_waits_for_the_reboot_after_updates() {
  run setup.sh >/dev/null
  touch "$MOCK_STATE/reboot-required"           # updates installed, reboot follows the probe
  run start.sh web >"$T/out"
  check "announced" has 'Updates need a reboot; waiting for the server to restart' "$T/out"
  check "probed again after the reboot" count_is 'cloud-init status' "$CALLS" 2
  check "ready" has '^Ready: ssh hetzner-claude-web' "$T/out"
}

test_reused_server_needing_a_reboot_only_warns() {
  run setup.sh >/dev/null
  run start.sh web >/dev/null
  touch "$MOCK_STATE/reboot-required" "$MOCK_STATE/reboot-required.keep"   # e.g. after unattended upgrades
  : > "$CALLS"
  run start.sh web >"$T/out" 2>"$T/err"
  check "no waiting" count_is 'cloud-init status' "$CALLS" 1
  check "warning" has "updates on the server need a reboot: make ssh MACHINE=web CMD='sudo reboot'" "$T/err"
  run check.sh web >"$T/check" || true
  check "check warns" has "WARN  updates need a reboot" "$T/check"
}

test_cloud_init_upgrades_and_reboots_only_if_required() {
  run setup.sh >/dev/null
  run start.sh web >/dev/null
  python3 -I - "$MOCK_STATE/hcloud/server/claude-web.user-data" <<'PY'
import sys, yaml
d = yaml.safe_load(open(sys.argv[1]))
assert d['package_upgrade'] is True
p = d['power_state']
assert p['mode'] == 'reboot' and p['condition'] == 'test -f /var/run/reboot-required', p
assert d['runcmd'][-1] == 'touch /etc/cloud/cloud-init.disabled', d['runcmd'][-1]
PY
}

test_cloud_init_writes_a_tmux_config_for_builder() {
  run setup.sh >/dev/null
  run start.sh web >/dev/null
  python3 -I - "$MOCK_STATE/hcloud/server/claude-web.user-data" <<'PY'
import sys, yaml
d = yaml.safe_load(open(sys.argv[1]))
f = [w for w in d['write_files'] if w['path'] == '/home/builder/.tmux.conf']
assert len(f) == 1, f
f = f[0]
# before the user exists write_files could not chown: it must be deferred
assert f['defer'] is True and f['owner'] == 'builder:builder', f
assert 'set -g mouse on' in f['content'] and 'bind -n M-1 select-window -t 1' in f['content']
PY
}

test_interactive_shells_start_in_the_work_directory() {
  run setup.sh >/dev/null
  ubuntu_home
  git init -q --bare "$T/origin.git"
  run start.sh web "$T/origin.git" >/dev/null
  h="$MOCK_STATE/remote-home"
  check "work dir recorded" test "$(cat "$h/.config/viper/workdir")" = "$T/mnt/origin"
  check "interactive shell from home" test "$(cd "$h" && HOME="$h" bash -i -c pwd 2>/dev/null </dev/null | tail -n1)" = "$T/mnt/origin"
  check "non-interactive shell stays" test "$(cd "$h" && HOME="$h" bash -c '. ~/.bashrc; pwd')" = "$h"
  check "elsewhere stays elsewhere" test "$(cd /tmp && HOME="$h" bash -i -c pwd 2>/dev/null </dev/null | tail -n1)" = /tmp
  run start.sh web "$T/origin.git" >/dev/null
  check "line added once" count_is 'viper: start in the work directory' "$h/.bashrc" 1
}

test_interrupted_first_run_is_not_treated_as_a_set_up_server() {
  run setup.sh >/dev/null
  MOCK_CF_WRITE_FAIL=1 fails run start.sh web >/dev/null 2>&1   # server created, run aborted at DNS
  check "server exists" test -f "$MOCK_STATE/hcloud/server/claude-web.json"
  check "not marked ready" test ! -f "$HOME/.local/state/hetzner-claude/ready-claude-web"
  touch "$MOCK_STATE/reboot-required"                          # its reboot is still to come
  : > "$CALLS"
  run start.sh web >"$T/out"
  check "waits for the reboot like a new server" has 'Updates need a reboot; waiting' "$T/out"
  check "marked ready now" test "$(cat "$HOME/.local/state/hetzner-claude/ready-claude-web")" = "$(cat "$HOME/.local/state/hetzner-claude/id-claude-web")"
}

test_transient_host_key_error_of_a_booting_server_is_retried() {
  run setup.sh >/dev/null
  MOCK_CF_WRITE_FAIL=1 fails run start.sh web >/dev/null 2>&1
  : > "$CALLS"
  MOCK_SSH_HOSTKEY_FAIL=1 fails run start.sh web >/dev/null 2>"$T/err"
  check "retried, not aborted at once" bash -c "[ \$(grep -c 'cloud-init status' '$CALLS') -gt 1 ]"
}

test_unreachable_error_shows_the_last_ssh_error() {
  run setup.sh >/dev/null
  MOCK_SSH_DOWN=1 fails run start.sh web >/dev/null 2>"$T/err"
  check "last error shown" has 'SSH not reachable; server retained. Last error: ssh: connect to host' "$T/err"
}

test_workspace_is_recorded_on_the_server_too() {
  run setup.sh >/dev/null
  run start.sh web >/dev/null
  run start.sh web >/dev/null
  check "recorded once" test "$(cat "$MOCK_STATE/remote-home/.config/viper/workspaces")" = "$T/mnt"
}

test_deploy_key_is_not_bypassed_by_the_github_token() {
  run setup.sh >/dev/null
  echo k > "$HOME/.ssh/deploy"
  printf 'GIT_DEPLOY_KEY_FILE=%s\nVIPER_GH_TOKEN=ghp_x\n' "$HOME/.ssh/deploy" > "$W/machines/web.env.local"
  MOCK_GH_NOREPO=1 run preflight.sh web git@github.com:o/r.git >"$T/pre"
  check "no token repo check for the key" bash -c "! grep -q 'token cannot access' '$T/pre'"
  run start.sh web >/dev/null 2>&1 || true
  check "old rewrite removed" has 'unset-all url\.https://github\.com/\.insteadOf' "$CALLS"
  check "no new rewrite" bash -c "! grep -q 'add url\.https://github\.com/\.insteadOf' '$CALLS'"
}

test_ipad_private_key_is_rejected() {
  run setup.sh >/dev/null
  printf 'IPAD_SSH_PUBLIC_KEY_FILE=%s\n' "$HOME/.ssh/hetzner-build" > "$W/machines/web.env.local"   # the private one
  fails run start.sh web >/dev/null 2>"$T/err"
  check "message" has 'must be a public key' "$T/err"
  check "no server" test ! -f "$MOCK_STATE/hcloud/server/claude-web.json"
}

test_connect_requests_a_terminal_for_commands_in_a_terminal() {
  command -v script >/dev/null || { echo "    skipped: no script(1)" >&3; return 0; }
  run setup.sh >/dev/null
  run start.sh web >/dev/null
  : > "$CALLS"
  run connect.sh web 'tmux attach -t claude' </dev/null >/dev/null
  check "no terminal without one" bash -c "! grep -q 'terminal requested' '$CALLS'"
  script -qec "$W/connect.sh web 'tmux attach -t claude'" /dev/null >/dev/null 2>&1
  check "terminal requested in a terminal" has 'terminal requested' "$CALLS"
}

test_homebrew_is_installed_and_on_the_path() {
  run setup.sh >/dev/null
  ubuntu_home
  run start.sh web >"$T/out"
  check "installer run" has 'raw\.githubusercontent\.com/Homebrew/install/HEAD/install\.sh' "$CALLS"
  check "non-interactive" has 'NONINTERACTIVE=1' "$CALLS"
  h="$MOCK_STATE/remote-home"
  check "brew in non-interactive shells" bash -c "HOME='$h' bash -c '. ~/.bashrc; command -v brew' | grep -q linuxbrew"
  check "brew in login shells" bash -c "HOME='$h' bash -lc 'command -v brew' | grep -q linuxbrew"
  run start.sh web >/dev/null
  check "path line once" count_is 'shellenv' "$h/.bashrc" 1
}

test_homebrew_can_be_skipped() {
  run setup.sh >/dev/null
  echo 'VIPER_HOMEBREW=0' > "$W/machines/web.env.local"
  run start.sh web >/dev/null
  check "no installer" bash -c "! grep -q 'Homebrew/install' '$CALLS'"
  run check.sh web >"$T/check"
  check "check does not expect it" bash -c "! grep -q 'Homebrew' '$T/check'"
}

test_failed_homebrew_install_only_warns() {
  run setup.sh >/dev/null
  MOCK_BREW_FAIL=1 run start.sh web >"$T/out" 2>"$T/err"
  check "warning" has 'WARNING: Homebrew installation failed' "$T/err"
  check "setup completed" has '^Ready: ssh hetzner-claude-web' "$T/out"
}

test_ready_marker_on_the_server_counts_for_another_computer() {
  run setup.sh >/dev/null
  run start.sh web >/dev/null
  check "server marker" test "$(cat "$MOCK_STATE/remote-home/.config/viper/ready")" = "$(cat "$HOME/.local/state/hetzner-claude/id-claude-web")"
  rm "$HOME/.local/state/hetzner-claude/ready-claude-web"     # like a second computer
  touch "$MOCK_STATE/reboot-required" "$MOCK_STATE/reboot-required.keep"   # later unattended upgrades
  : > "$CALLS"
  run start.sh web >/dev/null 2>"$T/err"
  check "no waiting" count_is 'cloud-init status' "$CALLS" 1
  check "only a warning" has 'updates on the server need a reboot' "$T/err"
}

test_wait_for_a_pending_reboot_is_capped() {
  run setup.sh >/dev/null
  MOCK_CF_WRITE_FAIL=1 fails run start.sh web >/dev/null 2>&1        # interrupted first run, no marker
  touch "$MOCK_STATE/reboot-required" "$MOCK_STATE/reboot-required.keep"   # the reboot never comes
  : > "$CALLS"
  run start.sh web >"$T/out" 2>"$T/err"
  check "capped" count_is 'cloud-init status' "$CALLS" 37
  check "then carries on" has '^Ready: ssh hetzner-claude-web' "$T/out"
  check "with a warning" has 'updates on the server need a reboot' "$T/err"
}

test_workspace_is_recorded_on_the_server_before_later_steps() {
  run setup.sh >/dev/null
  run start.sh web >/dev/null
  check "with the workspace directory creation" has 'sudo install -d -o builder -g builder .* ~/\.config/viper/workspaces' "$CALLS"
}

test_repo_given_once_is_remembered() {
  run setup.sh >/dev/null
  git init -q --bare "$T/origin.git"
  run start.sh web "$T/origin.git" >/dev/null
  run start.sh web >"$T/out"            # e.g. to refresh a token, without REPO=
  check "remembered" has 'Repository \(remembered from an earlier start\)' "$T/out"
  check "work dir kept" test "$(cat "$MOCK_STATE/remote-home/.config/viper/workdir")" = "$T/mnt/origin"
  run check.sh web >"$T/check"
  check "check checks it" has 'ok    repository .*/origin at ' "$T/check"
  printf 'DISCARD\nDELETE\n' | run stop.sh web >/dev/null 2>&1   # its remote is a local path: DISCARD
  check "forgotten with the machine" test ! -e "$HOME/.local/state/hetzner-claude/repo-claude-web"
}

test_workspace_recorded_on_server_survives_a_later_failure() {
  run setup.sh >/dev/null
  echo 'VIPER_GH_TOKEN=ghp_x' > "$W/machines/web.env.local"
  MOCK_GH_LOGIN_FAIL=1 fails run start.sh web >/dev/null 2>&1     # fails right after the workspace step
  check "recorded on the server" test "$(cat "$MOCK_STATE/remote-home/.config/viper/workspaces")" = "$T/mnt"
}

test_homebrew_installer_runs_when_brew_is_missing() {
  run setup.sh >/dev/null
  rm "$VIPER_BREW"                                               # a fresh server has no brew
  run start.sh web >"$T/out"
  check "installed by the (fake) installer" test -x "$VIPER_BREW"
  check "and reported" has '^homebrew: Homebrew ' "$HOME/.local/state/hetzner-claude/logs/claude-web.log"
}

test_old_server_counts_as_set_up_on_another_computer() {
  run setup.sh >/dev/null
  run start.sh web >/dev/null
  rm "$HOME/.local/state/hetzner-claude/ready-claude-web" "$MOCK_STATE/remote-home/.config/viper/ready"
  python3 -I - "$MOCK_STATE/hcloud/server/claude-web.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1])); d['created'] = '2026-01-01T00:00:00+00:00'; json.dump(d, open(sys.argv[1], 'w'))
PY
  : > "$CALLS"
  MOCK_SSH_HOSTKEY_FAIL=1 fails run start.sh web >/dev/null 2>"$T/err"
  check "fails fast" count_is 'cloud-init status' "$CALLS" 1
  check "named" has 'host key is not trusted' "$T/err"
}
