# shellcheck shell=bash
# Tests for stop.sh. Sourced by run.sh.

boot() { run setup.sh >/dev/null; run start.sh web >/dev/null; : > "$CALLS"; }

test_stop_requires_machine_name() {
  boot
  ! run stop.sh 2>/dev/null
}

test_stop_aborts_without_delete_confirmation() {
  boot
  ! echo no | run stop.sh web >/dev/null 2>&1
  check "server kept" test -f "$MOCK_STATE/hcloud/server/claude-web.json"
}

test_stop_deletes_server_and_dns() {
  boot
  echo DELETE | run stop.sh web >/dev/null
  check "server gone" test ! -f "$MOCK_STATE/hcloud/server/claude-web.json"
  check "dns removed" test "$(jq length "$MOCK_STATE/cf.json")" = 0
}

test_stop_only_touches_named_machine() {
  boot
  run start.sh api >/dev/null
  echo DELETE | run stop.sh web >/dev/null
  check "api server kept" test -f "$MOCK_STATE/hcloud/server/claude-api.json"
  check "api dns kept" has 'api\.test\.invalid' "$MOCK_STATE/cf.json"
}

test_stop_asks_extra_question_for_unsaved_work() {
  boot
  git init -q "$T/mnt/proj"
  echo x > "$T/mnt/proj/file"
  out=$(printf 'DISCARD\nno\n' | run stop.sh web 2>&1) || true
  check "uncommitted file listed" bash -c "grep -q 'file' <<<'$out'"
  check "warning" bash -c "grep -q 'UNSAVED WORK' <<<'$out'"
  check "server kept" test -f "$MOCK_STATE/hcloud/server/claude-web.json"
}

test_stop_unsaved_work_cancelled_without_discard() {
  boot
  git init -q "$T/mnt/proj"; echo x > "$T/mnt/proj/file"
  ! printf 'DELETE\nDELETE\n' | run stop.sh web >/dev/null 2>&1
  check "server kept" test -f "$MOCK_STATE/hcloud/server/claude-web.json"
}

test_stop_unsaved_work_deleted_after_discard_and_delete() {
  boot
  git init -q "$T/mnt/proj"; echo x > "$T/mnt/proj/file"
  printf 'DISCARD\nDELETE\n' | run stop.sh web >/dev/null 2>&1
  check "server gone" test ! -f "$MOCK_STATE/hcloud/server/claude-web.json"
}

test_stop_no_warning_without_repos() {
  boot
  out=$(echo DELETE | run stop.sh web 2>&1)
  check "no warning" bash -c "! grep -q 'UNSAVED WORK' <<<'$out'"
}

test_stop_unreachable_needs_force() {
  boot
  export MOCK_SSH_DOWN=1
  ! echo DELETE | run stop.sh web >/dev/null 2>"$T/err"
  check "hint" has '--force' "$T/err"
  check "server kept" test -f "$MOCK_STATE/hcloud/server/claude-web.json"
  echo DELETE | run stop.sh web --force >/dev/null 2>&1
  check "force deletes" test ! -f "$MOCK_STATE/hcloud/server/claude-web.json"
}

test_stop_of_absent_server_is_a_noop() {
  run setup.sh >/dev/null
  out=$(run stop.sh ghost)
  check "message" bash -c "grep -q 'already absent' <<<'$out'"
}

# repo with upstream, everything committed and pushed
pushed_repo() {
  git init -q --bare "$T/origin.git"
  git clone -q "$T/origin.git" "$T/mnt/proj" 2>/dev/null
  (cd "$T/mnt/proj" && echo 'build/' > .gitignore && git add .gitignore \
    && git -c user.email=t@t -c user.name=t commit -q -m init && git push -q -u origin HEAD 2>/dev/null)
}

test_stop_flags_stash_as_unsaved() {
  boot; pushed_repo
  (cd "$T/mnt/proj" && echo y >> .gitignore && git -c user.email=t@t -c user.name=t stash -q)
  out=$(printf 'no\n' | run stop.sh web 2>&1) || true
  check "stash listed" bash -c "grep -q 'stash@{0}' <<<'$out'"
  check "warning" bash -c "grep -q 'UNSAVED WORK' <<<'$out'"
}

test_stop_lists_ignored_files_without_blocking() {
  boot; pushed_repo
  mkdir "$T/mnt/proj/build"; echo x > "$T/mnt/proj/build/out"
  out=$(echo DELETE | run stop.sh web 2>&1)
  check "ignored listed" bash -c "grep -q 'Ignored by git' <<<'$out' && grep -q 'build/' <<<'$out'"
  check "no unsaved warning" bash -c "! grep -q 'UNSAVED WORK' <<<'$out'"
  check "deleted" test ! -f "$MOCK_STATE/hcloud/server/claude-web.json"
}

test_stop_reports_broken_repo_as_unsaved() {
  boot
  mkdir -p "$T/mnt/broken/.git"
  out=$(printf 'no\n' | run stop.sh web 2>&1) || true
  check "error shown" bash -c "grep -q 'cannot read git status' <<<'$out'"
  check "warning" bash -c "grep -q 'UNSAVED WORK' <<<'$out'"
  check "server kept" test -f "$MOCK_STATE/hcloud/server/claude-web.json"
}

test_stop_removes_local_ssh_config() {
  boot
  echo DELETE | run stop.sh web >/dev/null
  check "config file gone" test ! -f "$HOME/.ssh/config_hetzner_claude_web"
  check "include gone" bash -c "! grep -q config_hetzner_claude_web '$HOME/.ssh/config'"
}

test_stop_of_absent_server_cleans_up_leftovers() {
  boot
  rm "$MOCK_STATE/hcloud/server/claude-web.json"   # deleted in the Hetzner console
  run stop.sh web >/dev/null
  check "dns removed" test "$(jq length "$MOCK_STATE/cf.json")" = 0
  check "config file gone" test ! -f "$HOME/.ssh/config_hetzner_claude_web"
}
