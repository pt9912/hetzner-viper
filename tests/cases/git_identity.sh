# shellcheck shell=bash
# git user.name / user.email on the server, and docker buildx.

gitcfg() { git config --file "$MOCK_STATE/remote-home/.gitconfig" "$1"; }

test_git_identity_from_the_github_account() {
  run setup.sh >/dev/null
  export VIPER_GH_TOKEN=ghp_x
  run start.sh web >"$T/out"
  check "name" test "$(gitcfg user.name)" = 'Test User'
  check "noreply address" test "$(gitcfg user.email)" = '4242+tester@users.noreply.github.com'
  check "reported" has '^git: commits as Test User <4242\+tester@users\.noreply\.github\.com>$' "$T/out"
}

test_git_identity_login_when_the_account_has_no_name() {
  run setup.sh >/dev/null
  export VIPER_GH_TOKEN=ghp_x
  MOCK_GH_NONAME=1 run start.sh web >/dev/null
  check "login as name" test "$(gitcfg user.name)" = tester
}

test_explicit_git_identity_wins() {
  run setup.sh >/dev/null
  printf 'VIPER_GH_TOKEN=ghp_x\nVIPER_GIT_USER_NAME="Ada Lovelace"\nVIPER_GIT_USER_EMAIL=ada@example.invalid\n' > "$W/machines/web.env.local"
  run start.sh web >"$T/out"
  check "name" test "$(gitcfg user.name)" = 'Ada Lovelace'
  check "email" test "$(gitcfg user.email)" = ada@example.invalid
  check "no lookup needed" bash -c "! grep -q 'gh api' '$CALLS'"
}

test_only_the_missing_part_comes_from_github() {
  run setup.sh >/dev/null
  printf 'VIPER_GH_TOKEN=ghp_x\nVIPER_GIT_USER_NAME="Ada Lovelace"\n' > "$W/machines/web.env.local"
  run start.sh web >/dev/null
  check "own name" test "$(gitcfg user.name)" = 'Ada Lovelace'
  check "noreply address" test "$(gitcfg user.email)" = '4242+tester@users.noreply.github.com'
}

test_derived_identity_does_not_overwrite_a_value_already_on_the_server() {
  run setup.sh >/dev/null
  export VIPER_GH_TOKEN=ghp_x
  mkdir -p "$MOCK_STATE/remote-home"
  git config --file "$MOCK_STATE/remote-home/.gitconfig" user.name 'Set By Hand'
  run start.sh web >/dev/null
  check "kept" test "$(gitcfg user.name)" = 'Set By Hand'
  check "email added" test "$(gitcfg user.email)" = '4242+tester@users.noreply.github.com'
}

test_no_identity_without_settings_and_a_warning() {
  run setup.sh >/dev/null
  run preflight.sh web >"$T/pre"
  check "preflight says so" has 'info  no VIPER_GIT_USER_NAME / VIPER_GIT_USER_EMAIL: git commits on the server fail' "$T/pre"
  run start.sh web >"$T/out" 2>"$T/err"
  check "warned" has 'git user.name/user.email are not set on the server' "$T/err"
  check "nothing set" test -z "$(gitcfg user.name 2>/dev/null)"
}

test_git_identity_is_checked() {
  run setup.sh >/dev/null
  export VIPER_GH_TOKEN=ghp_x
  run start.sh web >/dev/null
  run check.sh web >"$T/ok"
  check "ok" has 'ok    git commits as Test User <4242\+tester@users\.noreply\.github\.com>' "$T/ok"
  git config --file "$MOCK_STATE/remote-home/.gitconfig" --unset user.email
  run check.sh web >"$T/warn"
  check "warns" has 'WARN  git user.name/user.email are not set on the server' "$T/warn"
}

test_bad_git_identity_is_refused_by_the_preflight() {
  run setup.sh >/dev/null
  printf 'VIPER_GIT_USER_NAME="A"\nVIPER_GIT_USER_EMAIL="not an address"\n' > "$W/machines/web.env.local"
  fails run preflight.sh web >"$T/out"
  check "email" has 'VIPER_GIT_USER_EMAIL is not an e-mail address' "$T/out"
}

test_docker_comes_from_dockers_repository_with_buildx_and_compose() {
  run setup.sh >/dev/null
  run start.sh web >"$T/out"
  check "package in the user-data" python3 -I -c "
import sys, yaml
d = yaml.safe_load(open('$MOCK_STATE/hcloud/server/claude-web.user-data'))
assert 'docker.io' not in d['packages'], d['packages']
run = [c for c in d['runcmd'] if 'download.docker.com' in c]
assert len(run) == 1 and 'docker-buildx-plugin' in run[0] and 'docker-compose-plugin' in run[0] and 'docker-ce ' in run[0], run
assert '9DC858229FC7DD38854AE2D88D81803C0EBFCD88' in run[0]
# set -e must stay inside the subshell, or a failure would skip the rest of runcmd
assert run[0].lstrip().startswith('(') and d['runcmd'][-1] == 'touch /etc/cloud/cloud-init.disabled'
assert d['runcmd'].index(run[0]) < d['runcmd'].index('systemctl enable --now docker')"
  check "asked for in the inventory" has 'bash -s -- .* docker-ce docker-ce-cli containerd\.io docker-buildx-plugin docker-compose-plugin' "$CALLS"
}
