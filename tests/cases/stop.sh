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
