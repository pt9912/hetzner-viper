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
  check "no nested preflight event in up" bash -c "! grep -qE ' up +preflight' '$T/ev'"
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
