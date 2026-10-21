# shellcheck shell=bash
# Homebrew taps, trust and formulae, and a Brewfile in the cloned repository.

brew_settings() {
  printf 'VIPER_BREW_TAPS="hashicorp/tap acme/tools=https://git.example.invalid/tools.git"\nVIPER_BREW_TRUST="hashicorp/tap acme/tools/widget cask:acme/tools/app"\nVIPER_BREW_PACKAGES="ripgrep hashicorp/tap/terraform"\n' > "$W/machines/web.env.local"
}

test_brew_taps_trust_and_installs_in_this_order() {
  run setup.sh >/dev/null
  brew_settings
  run start.sh web >"$T/out"
  check "tap" has '^brew: tap hashicorp/tap$' "$T/out"
  check "tap with url" has '^brew: tap acme/tools$' "$T/out"
  check "trust tap" has '^brew: trusted tap hashicorp/tap$' "$T/out"
  check "trust formula" has '^brew: trusted formula acme/tools/widget$' "$T/out"
  check "trust cask (prefix)" has '^brew: trusted cask acme/tools/app$' "$T/out"
  check "install" has '^brew: installed ripgrep hashicorp/tap/terraform$' "$T/out"
  check "custom tap url passed" has '^brew tap acme/tools https://git\.example\.invalid/tools\.git$' "$MOCK_STATE/brew.log"
  first() { grep -nE "^brew $1" "$MOCK_STATE/brew.log" | head -n1 | cut -d: -f1; }
  check "order: tap < trust < install" test "$(first tap)" -lt "$(first trust)" -a "$(first trust)" -lt "$(first install)"
}

test_brewfile_of_the_repository_is_applied() {
  run setup.sh >/dev/null
  git init -q --bare "$T/origin.git"
  git clone -q "$T/origin.git" "$T/seed" 2>/dev/null
  printf 'tap "acme/tools", trusted: true\nbrew "jq"\nbrew "acme/tools/widget"\n' > "$T/seed/Brewfile"
  (cd "$T/seed" && git add Brewfile && git -c user.email=t@t -c user.name=t commit -q -m brewfile && git push -q origin HEAD 2>/dev/null)
  echo "REPO_URL=$T/origin.git" > "$W/machines/web.env.local"
  run start.sh web >"$T/out"
  check "applied" has "^brew: $T/mnt/origin/Brewfile applied$" "$T/out"
  check "bundle with --no-upgrade" has 'brew bundle install --no-upgrade --file=' "$MOCK_STATE/brew.log"
  run check.sh web >"$T/check"
  check "check: Brewfile satisfied" has 'ok    Homebrew: Brewfile of the repository satisfied' "$T/check"
}

test_check_reports_missing_formula_and_trust() {
  run setup.sh >/dev/null
  brew_settings
  run start.sh web >/dev/null
  run check.sh web >"$T/ok"
  check "all present" has 'ok    Homebrew: taps, trust entries and formulae present' "$T/ok"
  sed -i '/^ripgrep$/d' "$MOCK_STATE/brew-formulae"
  sed -i '/^formula acme\/tools\/widget$/d' "$MOCK_STATE/brew-trust"
  fails run check.sh web >"$T/bad"
  check "missing listed" has 'FAIL  Homebrew: missing ripgrep trust:acme/tools/widget' "$T/bad"
}

test_failing_brew_steps_only_warn() {
  run setup.sh >/dev/null
  brew_settings
  MOCK_BREW_FAIL_TAP=acme/tools MOCK_BREW_FAIL_PKG=terraform run start.sh web >"$T/out" 2>"$T/err"
  check "tap failure shown" has '^brew: tap acme/tools FAILED$' "$T/out"
  check "install failure shown" has 'brew: install ripgrep hashicorp/tap/terraform FAILED' "$T/out"
  check "warning" has 'WARNING: some Homebrew taps, trust entries or formulae failed' "$T/err"
  check "setup completed" has '^Ready: ssh hetzner-claude-web' "$T/out"
}

test_preflight_validates_brew_settings() {
  run setup.sh >/dev/null
  printf 'VIPER_BREW_TAPS=justone\nVIPER_BREW_TRUST=weird:x/y\nVIPER_BREW_PACKAGES=-oops\n' > "$W/machines/web.env.local"
  fails run preflight.sh web >"$T/out"
  check "tap" has 'not a tap \(owner/repo or owner/repo=URL\): justone' "$T/out"
  check "trust" has 'VIPER_BREW_TRUST: not a tap .*: weird:x/y' "$T/out"
  check "formula" has 'not a formula .*: -oops' "$T/out"
  printf 'VIPER_HOMEBREW=0\nVIPER_BREW_PACKAGES=jq\n' > "$W/machines/web.env.local"
  fails run preflight.sh web >"$T/out2"
  check "needs homebrew" has 'need VIPER_HOMEBREW=1' "$T/out2"
}

test_brew_lists_may_span_several_lines() {
  run setup.sh >/dev/null
  printf 'VIPER_BREW_PACKAGES="ripgrep\n  fd\n  jq"\n' > "$W/machines/web.env.local"
  run start.sh web >"$T/out"
  check "all three" has '^brew: installed ripgrep fd jq$' "$T/out"
}
