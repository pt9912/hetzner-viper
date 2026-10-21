# shellcheck shell=bash
# events.log and logs/<server>.log, and make log.

events() { cat "$HOME/.local/state/hetzner-claude/events.log"; }
transcript() { cat "$HOME/.local/state/hetzner-claude/logs/claude-web.log"; }

test_up_and_down_are_logged() {
  run setup.sh >/dev/null
  run start.sh web >/dev/null
  events > "$T/ev"
  check "start" has ' claude-web +up +start \(cx53, fsn1, ubuntu-26\.04\)$' "$T/ev"
  check "created" has ' claude-web +up +server created id=[0-9]+$' "$T/ev"
  check "ip" has ' claude-web +up +ip=203\.0\.113\.[0-9]+ dns=claude-web\.test\.invalid$' "$T/ev"
  check "ready" has ' claude-web +up +ready \(work dir ' "$T/ev"
  echo DELETE | run stop.sh web >/dev/null
  events > "$T/ev"
  check "deleted" has ' claude-web +down +server deleted id=[0-9]+ ip=' "$T/ev"
  check "final" has ' claude-web +down +deleted$' "$T/ev"
  transcript > "$T/tr"
  check "transcript has both runs" bash -c "grep -c '^===== .* up claude-web$' '$T/tr' | grep -qx 1 && grep -q '^===== .* down claude-web$' '$T/tr'"
  check "output in the transcript" has '^Ready: ssh hetzner-claude-web' "$T/tr"
  check "nested preflight logged once" count_is '^Preflight OK\.' "$T/tr" 1
  check "files private" test "$(stat -c %a "$HOME/.local/state/hetzner-claude/events.log")$(stat -c %a "$HOME/.local/state/hetzner-claude/logs/claude-web.log")" = 600600
}

test_failures_and_cancellations_are_logged() {
  run setup.sh >/dev/null
  MOCK_SSH_DOWN=1 fails run start.sh web >/dev/null 2>&1
  events > "$T/ev"
  check "failed" has ' claude-web +up +FAILED \(exit 1; see logs/claude-web\.log\)$' "$T/ev"
  check "error in transcript" has 'SSH not reachable; server retained' <(transcript)
  run start.sh web >/dev/null
  echo no | fails run stop.sh web >/dev/null 2>&1
  check "cancelled" has ' claude-web +down +cancelled \(no DELETE\)$' <(events)
}

test_check_and_preflight_are_logged() {
  run setup.sh >/dev/null
  run preflight.sh web >/dev/null
  run start.sh web >/dev/null
  run check.sh web >/dev/null
  events > "$T/ev"
  check "preflight" has ' claude-web +preflight +preflight OK$' "$T/ev"
  check "check" has ' claude-web +check +check OK$' "$T/ev"
  check "preflight logged once (only the standalone run)" count_is 'preflight OK$' "$T/ev" 1
}

test_logs_contain_no_tokens() {
  run setup.sh >/dev/null
  export HCLOUD_TOKEN=hc_secret_1 CLOUDFLARE_API_TOKEN=cf_secret_2 VIPER_GH_TOKEN=gh_secret_3 VIPER_CLAUDE_TOKEN=sk-ant-oat01-secret_4
  run start.sh web >/dev/null
  run check.sh web >/dev/null
  echo DELETE | run stop.sh web >/dev/null
  check "no token in any log" bash -c "! grep -rqE 'secret_[0-9]' '$HOME/.local/state/hetzner-claude'"
}

test_make_log_shows_events() {
  run setup.sh >/dev/null
  run start.sh web >/dev/null
  run start.sh api >/dev/null
  make -s --no-print-directory -I "$ROOT" -f "$ROOT/Makefile" -C "$W" log > "$T/all"
  check "all machines" bash -c "grep -q ' claude-web ' '$T/all' && grep -q ' claude-api ' '$T/all'"
  make -s --no-print-directory -I "$ROOT" -f "$ROOT/Makefile" -C "$W" log MACHINE=web > "$T/web"
  check "only web" bash -c "grep -q ' claude-web ' '$T/web' && ! grep -q ' claude-api ' '$T/web'"
  make -s --no-print-directory -I "$ROOT" -f "$ROOT/Makefile" -C "$W" log MACHINE=web FULL=1 > "$T/full"
  check "full output" has '^Ready: ssh hetzner-claude-web' "$T/full"
}

test_interruption_is_logged() {
  run setup.sh >/dev/null
  run start.sh web >/dev/null
  # interrupted while stop.sh waits at its prompt (TERM: a test runner may start us with
  # SIGINT ignored, which bash cannot trap; a real Ctrl-C in a terminal goes the same way)
  /bin/sleep 5 | timeout -s TERM 2 "$W/stop.sh" web >/dev/null 2>&1 || true
  check "interrupted" has ' claude-web +down +interrupted$' <(events)
  check "not reported as FAILED" bash -c "! grep -q ' down .*FAILED' '$HOME/.local/state/hetzner-claude/events.log'"
}

test_credentials_in_repo_url_are_refused_and_never_logged() {
  run setup.sh >/dev/null
  fails run start.sh web 'https://oauth2:tok_secret_9@git.example.invalid/o/r.git' >/dev/null 2>"$T/err"
  check "refused" has 'REPO_URL contains credentials \(https://\*\*\*@git\.example\.invalid/o/r\.git\)' "$T/err"
  check "not in events or transcript" bash -c "! grep -rq tok_secret_9 '$HOME/.local/state/hetzner-claude'"
  check "no server" test ! -f "$MOCK_STATE/hcloud/server/claude-web.json"
}

test_stale_transcript_variables_do_not_suppress_logging() {
  run setup.sh >/dev/null
  run start.sh web >/dev/null
  export VIPER_TRANSCRIPT=/nonexistent/x.log VIPER_TRANSCRIPT_PID=999999 VIPER_ACTION=up   # e.g. inherited by a VS Code terminal
  echo DELETE | run stop.sh web >/dev/null
  check "own transcript" bash -c "grep -q '^===== .* down claude-web$' '$HOME/.local/state/hetzner-claude/logs/claude-web.log'"
  check "own events" has ' claude-web +down +deleted$' <(events)
}

test_make_force_and_full_need_1() {
  mkdir -p "$HOME/.local/state/hetzner-claude/logs"; echo x > "$HOME/.local/state/hetzner-claude/logs/claude-web.log"
  make -s --no-print-directory -I "$ROOT" -f "$ROOT/Makefile" -C "$W" log MACHINE=web FULL=0 > "$T/out"
  check "FULL=0 shows events, not the log" bash -c "! grep -qx x '$T/out'"
  make -s --no-print-directory -I "$ROOT" -f "$ROOT/Makefile" -C "$W" down MACHINE=web FORCE=0 >/dev/null 2>&1 || true
  check "FORCE=0 passes no --force" bash -c "! grep -qx -- --force '$MOCK_STATE'/docker-call-1"
}

test_inventory_of_the_server_is_logged() {
  run setup.sh >/dev/null
  printf 'VIPER_BREW_TAPS="acme/tools"\nVIPER_BREW_TRUST="acme/tools"\nVIPER_BREW_PACKAGES="ripgrep acme/tools/widget"\n' > "$W/machines/web.env.local"
  run start.sh web >/dev/null
  transcript > "$T/tr"
  check "section" has '^== installed on the server ==$' "$T/tr"
  check "os" has '^os: .*, kernel ' "$T/tr"
  check "apt list asked for the cloud-init packages" has 'bash -s -- .* docker\.io git gh make build-essential' "$CALLS"
  check "homebrew" has '^homebrew: Homebrew ' "$T/tr"
  check "taps" has '^brew taps: .*acme/tools' "$T/tr"
  check "trusted" has '^brew trusted: \{"taps":\["acme/tools"\]\}' "$T/tr"
  check "formulae with versions" has '^brew formulae \(installed on request\): ripgrep 1\.0, widget 1\.0$' "$T/tr"
}
