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
  check "default elsewhere" has 'claude-big .*--type cx53' "$CALLS"
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
