# shellcheck shell=bash
# Claude Code with another model behind an Anthropic-compatible API (Ollama cloud, vLLM, ...).

gateway_settings() {
  printf 'VIPER_ANTHROPIC_BASE_URL=https://llm.test.invalid\nVIPER_ANTHROPIC_AUTH_TOKEN=olk_secret_9\nVIPER_CLAUDE_MODEL=%s\n' "${1:-minimax-m3.1-flash}" > "$W/machines/web.env.local"
}
# value of a variable in a non-interactive shell on the (fake) server
remote_env() { HOME="$MOCK_STATE/remote-home" bash -c '. ~/.bashrc >/dev/null 2>&1; printf %s "${'"$1"'-<unset>}"'; }

test_gateway_settings_reach_the_shells_on_the_server() {
  run setup.sh >/dev/null
  gateway_settings
  echo 'VIPER_CLAUDE_MAX_CONTEXT_TOKENS=1048576' >> "$W/machines/web.env.local"
  run start.sh web >"$T/out"
  check "message" has 'claude: using model minimax-m3.1-flash at https://llm.test.invalid' "$T/out"
  check "base url" test "$(remote_env ANTHROPIC_BASE_URL)" = https://llm.test.invalid
  check "auth token" test "$(remote_env ANTHROPIC_AUTH_TOKEN)" = olk_secret_9
  check "api key empty" test "$(remote_env ANTHROPIC_API_KEY)" = ""
  for v in ANTHROPIC_MODEL ANTHROPIC_DEFAULT_OPUS_MODEL ANTHROPIC_DEFAULT_SONNET_MODEL ANTHROPIC_DEFAULT_HAIKU_MODEL CLAUDE_CODE_SUBAGENT_MODEL; do
    check "$v" test "$(remote_env "$v")" = minimax-m3.1-flash
  done
  check "context window" test "$(remote_env CLAUDE_CODE_MAX_CONTEXT_TOKENS)" = 1048576
  check "token not on any command line" bash -c "! grep -q olk_secret_9 '$CALLS'"
}

test_switching_from_subscription_to_gateway_drops_the_oauth_token() {
  run setup.sh >/dev/null
  VIPER_CLAUDE_TOKEN=sk-ant-oat01-x run start.sh web >/dev/null
  check "first: subscription" test "$(remote_env CLAUDE_CODE_OAUTH_TOKEN)" = sk-ant-oat01-x
  gateway_settings
  run start.sh web >/dev/null
  check "then: no oauth token" test "$(remote_env CLAUDE_CODE_OAUTH_TOKEN)" = '<unset>'
}

test_preflight_checks_the_gateway_with_a_real_request() {
  run setup.sh >/dev/null
  gateway_settings 'glm-5.3-flash[1m]'
  run preflight.sh web >"$T/out"
  check "answers" has 'ok    https://llm.test.invalid answers with model glm-5.3-flash ' "$T/out"
  check "[1m] stripped from the model ID" has '"model": "glm-5.3-flash"' "$CALLS"
  check "token as header file, not argv" bash -c "! grep -q olk_secret_9 '$CALLS'"
  MOCK_LLM_BAD_TOKEN=1 fails run preflight.sh web >"$T/bad"
  check "bad token" has 'rejects VIPER_ANTHROPIC_AUTH_TOKEN \(HTTP 401: invalid api key\)' "$T/bad"
  MOCK_LLM_BAD_MODEL=glm-5.3-flash fails run preflight.sh web >"$T/model"
  check "bad model" has "model glm-5.3-flash or the Messages API not found \(HTTP 404: model 'glm-5.3-flash' not found\)" "$T/model"
}

test_gateway_and_subscription_token_together_fail_before_anything() {
  run setup.sh >/dev/null
  gateway_settings
  echo 'VIPER_CLAUDE_TOKEN=sk-ant-oat01-x' >> "$W/machines/web.env.local"
  fails run start.sh web >/dev/null 2>"$T/err"
  check "message" has 'set either VIPER_CLAUDE_TOKEN or VIPER_ANTHROPIC_BASE_URL, not both' "$T/err"
  check "no server" test ! -f "$MOCK_STATE/hcloud/server/claude-web.json"
}

test_gateway_needs_token_and_model_and_a_url() {
  run setup.sh >/dev/null
  printf 'VIPER_ANTHROPIC_BASE_URL=llm.test.invalid\n' > "$W/machines/web.env.local"
  fails run preflight.sh web >"$T/out"
  check "token" has 'VIPER_ANTHROPIC_AUTH_TOKEN is required' "$T/out"
  check "model" has 'VIPER_CLAUDE_MODEL is required' "$T/out"
  check "url" has 'must start with https://' "$T/out"
}

test_plain_http_gateway_is_warned_about() {
  run setup.sh >/dev/null
  gateway_settings
  sed -i 's|https://llm|http://llm|' "$W/machines/web.env.local"
  run preflight.sh web >"$T/out"
  check "warning" has 'WARN  VIPER_ANTHROPIC_BASE_URL is not https' "$T/out"
}

test_check_reports_the_gateway_model() {
  run setup.sh >/dev/null
  gateway_settings
  run start.sh web >/dev/null
  run check.sh web >"$T/out"
  check "gateway answers" has 'ok    Claude Code .*: model minimax-m3.1-flash at https://llm.test.invalid answers' "$T/out"
  check "summary" has '^Check OK' "$T/out"
}

test_vscode_does_not_inherit_the_gateway_token() {
  run setup.sh >/dev/null
  gateway_settings
  run start.sh web >/dev/null
  check "no tokens for VS Code" test ! -s "$MOCK_STATE/code-env"
}

test_gateway_url_with_credentials_is_not_shown() {
  run setup.sh >/dev/null
  printf 'VIPER_ANTHROPIC_BASE_URL=https://user:gw_secret_5@llm.test.invalid\nVIPER_ANTHROPIC_AUTH_TOKEN=k\nVIPER_CLAUDE_MODEL=m\n' > "$W/machines/web.env.local"
  run start.sh web >"$T/out" 2>&1 || true
  run check.sh web >>"$T/out" 2>&1 || true
  check "redacted" has 'https://\*\*\*@llm\.test\.invalid' "$T/out"
  check "never shown or logged" bash -c "! grep -rq gw_secret_5 '$T/out' '$HOME/.local/state/hetzner-claude'"
}
