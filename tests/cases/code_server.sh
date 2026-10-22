# shellcheck shell=bash
# code-server (VS Code in the browser), opt-in with VIPER_CODE_SERVER=1.

cs_settings() {
  # the file is sourced by bash: quote the value like a user has to
  printf 'VIPER_CODE_SERVER=1\nVIPER_CODE_SERVER_PASSWORD=%q\n' "${1:-pw-with-12-chars}" > "$W/machines/web.env.local"
}

test_code_server_is_off_by_default() {
  run setup.sh >/dev/null
  run start.sh web >"$T/out"
  check "not installed" bash -c "! grep -q 'code-server' '$CALLS'"
  check "no unit" test ! -s "$MOCK_STATE/systemctl.log"
}

test_code_server_is_installed_configured_and_started() {
  run setup.sh >/dev/null
  cs_settings 'p@ss"w0rd"\#1:2-ok'
  run start.sh web >"$T/out"
  check "official installer" has 'curl -fsSL -o .* https://code-server\.dev/install\.sh' "$CALLS"
  check "bound to localhost only" has '^bind-addr: 127\.0\.0\.1:8080$' "$MOCK_STATE/remote-home/.config/code-server/config.yaml"
  check "password auth" has '^auth: password$' "$MOCK_STATE/remote-home/.config/code-server/config.yaml"
  # the file is YAML: any character of the password must survive
  check "password quoted" python3 -I -c "
import sys, yaml
c = yaml.safe_load(open('$MOCK_STATE/remote-home/.config/code-server/config.yaml'))
assert c['password'] == 'p@ss\"w0rd\"\\\\#1:2-ok', c
assert c['bind-addr'] == '127.0.0.1:8080' and c['cert'] is False"
  check "file is private" test "$(stat -c %a "$MOCK_STATE/remote-home/.config/code-server/config.yaml")" = 600
  check "unit enabled and restarted" has 'systemctl enable code-server@builder' "$MOCK_STATE/systemctl.log"
  check "restart" has 'systemctl restart code-server@builder' "$MOCK_STATE/systemctl.log"
  check "tunnel hint" has 'code-server: running .*tunnel: ssh -L 8080:127\.0\.0\.1:8080 hetzner-claude-web' "$T/out"
  check "in the inventory" has '^code-server: 4\.96\.4' "$T/out"
}

test_code_server_password_never_in_argv_logs_or_vscode() {
  run setup.sh >/dev/null
  cs_settings 'secret-pw-0042'
  run start.sh web >/dev/null
  check "not in the calls" bash -c "! grep -q secret-pw-0042 '$CALLS'"
  check "not in logs" bash -c "! grep -rq secret-pw-0042 '$HOME/.local/state/hetzner-claude'"
  check "not in user-data" bash -c "! grep -q secret-pw-0042 '$MOCK_STATE'/hcloud/server/*.user-data"
  check "not given to VS Code" test ! -s "$MOCK_STATE/code-env"
}

test_code_server_needs_a_good_password() {
  run setup.sh >/dev/null
  printf 'VIPER_CODE_SERVER=1\n' > "$W/machines/web.env.local"
  fails run preflight.sh web >"$T/none"
  check "required" has 'VIPER_CODE_SERVER=1 needs VIPER_CODE_SERVER_PASSWORD' "$T/none"
  cs_settings short
  fails run preflight.sh web >"$T/short"
  check "length" has 'at least 12 characters' "$T/short"
  cs_settings 'has a space-1234'
  fails run preflight.sh web >"$T/space"
  check "no spaces" has 'no spaces or line breaks' "$T/space"
  printf 'VIPER_CODE_SERVER=yes\n' > "$W/machines/web.env.local"
  fails run preflight.sh web >"$T/value"
  check "0 or 1" has 'VIPER_CODE_SERVER must be 0 or 1: yes' "$T/value"
  cs_settings
  run preflight.sh web >"$T/ok"
  check "announced" has 'ok    code-server will be installed .*127\.0\.0\.1:8080' "$T/ok"
  printf 'VIPER_CODE_SERVER_PASSWORD=pw-with-12-chars\n' > "$W/machines/web.env.local"
  run preflight.sh web >"$T/ignored"
  check "password without the option" has 'info  VIPER_CODE_SERVER_PASSWORD is set but VIPER_CODE_SERVER is not 1' "$T/ignored"
}

test_code_server_failures_are_not_fatal() {
  run setup.sh >/dev/null
  cs_settings
  MOCK_CODE_SERVER_FAIL=1 run start.sh web >"$T/out" 2>"$T/err"
  check "install warning" has 'WARNING: code-server installation failed' "$T/err"
  check "still ready" has '^Ready: ssh hetzner-claude-web' "$T/out"
  check "no config without the program" test ! -e "$MOCK_STATE/remote-home/.config/code-server/config.yaml"
  rm -f "$MOCK_STATE/code-server-installed"
  MOCK_SYSTEMCTL_FAIL=1 run start.sh web >"$T/out2" 2>"$T/err2"
  check "start warning" has 'WARNING: code-server could not be configured or started' "$T/err2"
  check "still ready (2)" has '^Ready: ssh hetzner-claude-web' "$T/out2"
}

test_check_reports_code_server() {
  run setup.sh >/dev/null
  cs_settings
  run start.sh web >/dev/null
  run check.sh web >"$T/out"
  check "ok" has 'ok    code-server 4\.96\.4 .* on 127\.0\.0\.1:8080 \(tunnel: ssh -L 8080:127\.0\.0\.1:8080 hetzner-claude-web\)' "$T/out"
  MOCK_CODE_SERVER_DOWN=1 fails run check.sh web >"$T/down"
  check "not running" has 'FAIL  code-server 4\.96\.4 .* is not running' "$T/down"
  rm "$MOCK_STATE/code-server-installed"
  fails run check.sh web >"$T/gone"
  check "not installed" has 'FAIL  code-server not installed' "$T/gone"
  printf 'VIPER_CODE_SERVER=0\n' > "$W/machines/web.env.local"
  run check.sh web >"$T/off"
  check "nothing checked when off" bash -c "! grep -q 'code-server' '$T/off'"
}
