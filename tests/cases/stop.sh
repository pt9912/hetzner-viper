# shellcheck shell=bash
# Tests for stop.sh. Sourced by run.sh.

boot() { run setup.sh >/dev/null; run start.sh web >/dev/null; : > "$CALLS"; }

test_stop_requires_machine_name() {
  boot
  fails run stop.sh 2>/dev/null
}

test_stop_aborts_without_delete_confirmation() {
  boot
  echo no | fails run stop.sh web >/dev/null 2>&1
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
  check "api dns kept" has 'claude-api\.test\.invalid' "$MOCK_STATE/cf.json"
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
  printf 'DELETE\nDELETE\n' | fails run stop.sh web >/dev/null 2>&1
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
  echo DELETE | fails run stop.sh web >/dev/null 2>"$T/err"
  check "hint" has '--force' "$T/err"
  check "server kept" test -f "$MOCK_STATE/hcloud/server/claude-web.json"
  echo DELETE | run stop.sh web --force >/dev/null 2>&1
  check "force deletes" test ! -f "$MOCK_STATE/hcloud/server/claude-web.json"
}

test_stop_of_absent_server_asks_before_cleanup() {
  run setup.sh >/dev/null
  echo no | fails run stop.sh ghost >/dev/null 2>&1
  echo CLEANUP | run stop.sh ghost >/dev/null
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
  check "error shown" bash -c "grep -q 'ERROR: cannot read git' <<<'$out'"
  check "warning" bash -c "grep -q 'UNSAVED WORK' <<<'$out'"
  check "server kept" test -f "$MOCK_STATE/hcloud/server/claude-web.json"
}

test_stop_removes_local_ssh_config() {
  boot
  echo DELETE | run stop.sh web >/dev/null
  check "config file gone" test ! -f "$HOME/.ssh/config_hetzner_claude-web"
  check "include gone" bash -c "! grep -q config_hetzner_claude-web '$HOME/.ssh/config'"
}

test_stop_of_absent_server_cleans_up_leftovers() {
  boot
  rm "$MOCK_STATE/hcloud/server/claude-web.json"   # deleted in the Hetzner console
  echo CLEANUP | run stop.sh web >/dev/null
  check "dns removed" test "$(jq length "$MOCK_STATE/cf.json")" = 0
  check "config file gone" test ! -f "$HOME/.ssh/config_hetzner_claude-web"
}

test_stop_wrong_project_without_confirmation_changes_nothing() {
  boot
  rm "$MOCK_STATE/hcloud/server/claude-web.json"   # e.g. token of another project
  echo no | fails run stop.sh web >/dev/null 2>&1
  check "dns kept" test "$(jq length "$MOCK_STATE/cf.json")" = 1
  check "config kept" test -f "$HOME/.ssh/config_hetzner_claude-web"
  check "pin kept" test -f "$HOME/.ssh/known_hosts_hetzner_claude-web"
}

test_stop_absent_without_recorded_ip_leaves_dns() {
  boot
  rm "$MOCK_STATE/hcloud/server/claude-web.json" "$HOME/.local/state/hetzner-claude/ip-claude-web"
  echo CLEANUP | run stop.sh web >/dev/null
  check "dns untouched" test "$(jq length "$MOCK_STATE/cf.json")" = 1
}

test_stop_finds_repos_outside_workspace() {
  boot
  git init -q "$MOCK_STATE/remote-home/scratch"; echo x > "$MOCK_STATE/remote-home/scratch/f"
  git init -q "$MOCK_STATE/remote-tmp/exp"
  out=$(printf 'no
' | run stop.sh web 2>&1) || true
  check "home repo checked" bash -c "grep -q '== $MOCK_STATE/remote-home/scratch ==' <<<'$out'"
  check "tmp repo checked" bash -c "grep -q '== $MOCK_STATE/remote-tmp/exp ==' <<<'$out'"
  check "warning" bash -c "grep -q 'UNSAVED WORK' <<<'$out'"
}

test_stop_flags_loose_files_in_workspace() {
  boot
  echo notes > "$T/mnt/notes.txt"
  out=$(printf 'no
' | run stop.sh web 2>&1) || true
  check "listed" bash -c "grep -q 'Not in any git repository' <<<'$out' && grep -q notes.txt <<<'$out'"
  check "warning" bash -c "grep -q 'UNSAVED WORK' <<<'$out'"
}

test_stop_flags_missing_workspace() {
  boot
  rmdir "$T/mnt"
  out=$(printf 'no
' | run stop.sh web 2>&1) || true
  check "error" bash -c "grep -q 'does not exist' <<<'$out'"
  check "warning" bash -c "grep -q 'UNSAVED WORK' <<<'$out'"
}

test_stop_answers_from_pipe_reach_the_prompts() {
  boot; pushed_repo
  echo x > "$T/mnt/proj/dirty"
  printf 'DISCARD
DELETE
' | run stop.sh web >/dev/null
  check "deleted" test ! -f "$MOCK_STATE/hcloud/server/claude-web.json"
}

test_stop_flags_unpushed_commits_on_other_branch() {
  boot; pushed_repo
  (cd "$T/mnt/proj" && git checkout -q -b feature && echo f > f && git add f \
    && git -c user.email=t@t -c user.name=t commit -q -m wip && git checkout -q -)
  out=$(printf 'no\n' | run stop.sh web 2>&1) || true
  check "commit listed" bash -c "grep -q 'wip' <<<'$out'"
  check "warning" bash -c "grep -q 'UNSAVED WORK' <<<'$out'"
}

test_stop_checks_linked_worktrees() {
  boot; pushed_repo
  git -C "$T/mnt/proj" worktree add -q "$T/elsewhere" -b wt 2>/dev/null
  echo dirty > "$T/elsewhere/new"
  out=$(printf 'no\n' | run stop.sh web 2>&1) || true
  check "worktree checked" bash -c "grep -q '== $T/elsewhere ==' <<<'$out'"
  check "warning" bash -c "grep -q 'UNSAVED WORK' <<<'$out'"
}

test_stop_clean_pushed_repo_needs_no_discard() {
  boot; pushed_repo
  echo DELETE | run stop.sh web >/dev/null
  check "deleted" test ! -f "$MOCK_STATE/hcloud/server/claude-web.json"
}

test_stop_hcloud_error_keeps_everything() {
  boot
  export MOCK_HCLOUD_FAIL=1
  echo DELETE | fails run stop.sh web >/dev/null 2>"$T/err"
  check "message" has 'list failed' "$T/err"
  check "dns kept" test "$(jq length "$MOCK_STATE/cf.json")" = 1
  check "ssh config kept" test -f "$HOME/.ssh/config_hetzner_claude-web"
}

test_stop_checks_workspace_from_start() {
  boot
  git init -q "$T/mnt/proj"; echo x > "$T/mnt/proj/file"
  echo "WORKSPACE_DIR=$T/other" >> "$W/.env.local"
  out=$(printf 'no\n' | run stop.sh web 2>&1) || true
  check "old workspace checked" bash -c "grep -q 'UNSAVED WORK' <<<'$out'"
}

test_stop_dns_failure_still_deletes_with_warning() {
  boot
  export MOCK_CF_FAIL=1
  echo DELETE | run stop.sh web >/dev/null 2>"$T/err"
  check "deleted" test ! -f "$MOCK_STATE/hcloud/server/claude-web.json"
  check "warning" has 'DNS removal failed' "$T/err"
}
