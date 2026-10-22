# shellcheck shell=bash
# preflight.sh and the up-front settings check: problems are found before anything exists.

nothing_created() {
  ! grep -qE '^hcloud (ssh-key|firewall|server) (create|add-rule|delete)|^curl .*-X (POST|PUT|DELETE)' "$CALLS"
}

test_preflight_ok_and_read_only() {
  run setup.sh >/dev/null
  run preflight.sh web >"$T/out"
  check "summary" has '^Preflight OK' "$T/out"
  check "availability checked" has 'cpx32 can currently be ordered in fsn1' "$T/out"
  check "type summary" has 'server type cpx32 exists \(4 vCPU, 8 GB RAM, 160 GB disk\), architecture x86' "$T/out"
  check "nothing created" nothing_created
}

test_missing_settings_reported_together_before_anything() {
  run setup.sh >/dev/null
  unset CLOUDFLARE_API_TOKEN CLOUDFLARE_ZONE_ID
  fails run start.sh web >/dev/null 2>"$T/err"
  check "all listed" has 'Missing settings: CLOUDFLARE_API_TOKEN CLOUDFLARE_ZONE_ID' "$T/err"
  check "nothing created" nothing_created
}

test_defaults_fill_settings_missing_from_env_local() {
  run setup.sh >/dev/null
  sed -i '/^SERVER_TYPE=/d; /^SERVER_LOCATION=/d; /^SERVER_IMAGE=/d; /^SSH_KEY=/d' "$W/.env.local"
  run start.sh web >/dev/null
  check "defaults used" has 'server create --name claude-web --type cpx32 --location fsn1 --image ubuntu-26.04' "$CALLS"
}

test_bad_dns_domain_rejected() {
  run setup.sh >/dev/null
  echo 'DNS_DOMAIN=https://Example.com/' >> "$W/.env.local"
  fails run start.sh web >/dev/null 2>"$T/err"
  check "message" has 'DNS_DOMAIN must be a domain' "$T/err"
}

test_stop_without_zone_id_fails_before_deleting() {
  run setup.sh >/dev/null; run start.sh web >/dev/null
  unset CLOUDFLARE_ZONE_ID
  echo DELETE | fails run stop.sh web >/dev/null 2>"$T/err"
  check "message" has 'Missing settings: CLOUDFLARE_ZONE_ID' "$T/err"
  check "server kept" test -f "$MOCK_STATE/hcloud/server/claude-web.json"
}

test_sold_out_server_type_fails_before_anything() {
  run setup.sh >/dev/null
  echo 'SERVER_TYPE=cpx02' > "$W/machines/web.env.local"
  export MOCK_SOLD_OUT=cpx02
  fails run start.sh web >/dev/null 2>"$T/err"
  check "message" has 'cpx02 cannot currently be ordered in fsn1 \(available in: nbg1\)' "$T/err"
  check "nothing created" nothing_created
}

test_unknown_type_location_and_image_reported() {
  run setup.sh >/dev/null
  printf 'SERVER_TYPE=cx11\n' > "$W/machines/a.env.local"
  printf 'SERVER_LOCATION=xyz1\n' > "$W/machines/b.env.local"
  printf 'SERVER_TYPE=cax11\nSERVER_IMAGE=ubuntu-22.04\n' > "$W/machines/c.env.local"
  run preflight.sh a >"$T/a" || true; run preflight.sh b >"$T/b" || true; run preflight.sh c >"$T/c" || true
  check "type" has 'FAIL  server type cx11 does not exist' "$T/a"
  check "location" has 'FAIL  location xyz1 does not exist' "$T/b"
  check "image for arch" has 'FAIL  image ubuntu-22.04 does not exist for arm' "$T/c"
}

test_foreign_dns_record_fails_before_anything() {
  run setup.sh >/dev/null
  echo '[{"id":"x1","type":"A","name":"claude-web.test.invalid","content":"198.51.100.1","comment":"mine"}]' > "$MOCK_STATE/cf.json"
  fails run start.sh web >/dev/null 2>"$T/err"
  check "message" has 'not managed by this setup' "$T/err"
  check "nothing created" nothing_created
}

test_dns_domain_outside_zone_fails() {
  run setup.sh >/dev/null
  export MOCK_CF_ZONE=other.invalid
  fails run preflight.sh web >"$T/out"
  check "message" has 'not in Cloudflare zone other.invalid' "$T/out"
}

test_github_token_checks() {
  run setup.sh >/dev/null
  export VIPER_GH_TOKEN=ghp_x
  run preflight.sh web https://github.com/o/r.git >"$T/ok"
  check "valid" has 'VIPER_GH_TOKEN valid for tester' "$T/ok"
  check "repo access" has 'token can access o/r' "$T/ok"
  check "push access" has 'token can push to o/r' "$T/ok"
  check "push check as git does it" has 'curl .*https://github\.com/o/r\.git/info/refs\?service=git-receive-pack' "$CALLS"
  check "token not in argv" bash -c "! grep -q ghp_x '$CALLS'"
  MOCK_GH_READONLY=1 fails run preflight.sh web https://github.com/o/r.git >"$T/readonly"
  check "read-only token" has 'token cannot push to o/r .*Contents must be Read and write' "$T/readonly"
  MOCK_GH_SCOPES='repo, workflow' fails run preflight.sh web >"$T/scopes"
  check "classic token scopes" has 'lacks scope read:org' "$T/scopes"
  MOCK_GH_NOREPO=1 fails run preflight.sh web https://github.com/o/r.git >"$T/norepo"
  check "no repo access" has 'token cannot access o/r' "$T/norepo"
  MOCK_GH_BAD=1 fails run preflight.sh web >"$T/bad"
  check "rejected" has 'VIPER_GH_TOKEN rejected' "$T/bad"
}

test_unreachable_repository_fails_before_anything() {
  run setup.sh >/dev/null
  fails run start.sh web "$T/does-not-exist.git" >/dev/null 2>"$T/err"
  check "message" has 'not reachable without credentials' "$T/err"
  check "nothing created" nothing_created
}

test_bad_allowed_cidrs_reported() {
  run setup.sh >/dev/null
  echo 'SSH_ALLOWED_CIDRS=10.0.0.0/8,anywhere' >> "$W/.env.local"
  fails run preflight.sh web >"$T/out"
  check "message" has 'not a network: anywhere' "$T/out"
}

test_readable_config_with_tokens_is_warned_about() {
  run setup.sh >/dev/null
  chmod 644 "$W/.env.local"
  run preflight.sh web >"$T/out"
  check "warning" has 'WARN  .env.local is readable by other users' "$T/out"
  chmod 600 "$W/.env.local"
  run preflight.sh web >"$T/out2"
  check "no warning" bash -c "! grep -q WARN '$T/out2'"
}

test_setup_creates_env_local_private() {
  rm "$W/.env.local"
  run setup.sh >/dev/null 2>&1 || true   # stops at the empty DNS_DOMAIN of the template
  check "mode 600" test "$(stat -c %a "$W/.env.local")" = 600
}

test_type_not_offered_in_location() {
  run setup.sh >/dev/null
  printf 'SERVER_TYPE=cpx02\nSERVER_LOCATION=hel1\n' > "$W/machines/web.env.local"
  fails run preflight.sh web >"$T/out"
  check "message" has 'cpx02 is not offered in hel1 \(available in: fsn1, nbg1\)' "$T/out"
}

test_deprecated_type_is_warned_about() {
  run setup.sh >/dev/null
  export MOCK_DEPRECATED=cpx32
  run preflight.sh web >"$T/out"
  check "warning" has 'WARN  cpx32 is deprecated in fsn1 and unavailable after 2027-01-01' "$T/out"
}
