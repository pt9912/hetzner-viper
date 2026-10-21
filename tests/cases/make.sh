# shellcheck shell=bash
# Makefile targets with a fake docker (tests/mocks/docker): the right script runs in the
# container, arguments survive as single arguments, MACHINE is required.

# mk <target> [VAR=value ...]: run make in the repo, quietly
mk() { make -s --no-print-directory -C "$ROOT" "$@" >/dev/null 2>"$T/make-err"; }
# inner_cmd <n>: the command run in the container by docker call <n> (after the image name)
inner_cmd() { sed -n '/^hetzner-claude-run$/,$p' "$MOCK_STATE/docker-call-$1" | tail -n +2; }

test_make_targets_run_the_right_script_in_the_container() {
  mk setup
  check "build first" grep -qx build "$MOCK_STATE/docker-call-0"
  check "setup" test "$(inner_cmd 1)" = ./setup.sh
  rm "$MOCK_STATE"/docker-call-*
  mk preflight MACHINE=web
  check "preflight" test "$(inner_cmd 1 | head -n2 | tr '\n' ' ')" = "./preflight.sh web "
  rm "$MOCK_STATE"/docker-call-*
  mk down MACHINE=web FORCE=1
  check "down --force" test "$(inner_cmd 1 | tr '\n' ' ')" = "./stop.sh web --force "
}

test_make_up_keeps_repo_url_one_argument() {
  mk up MACHINE=web 'REPO=https://x.org/a b.git?x=1&y=2;z'
  check "url is one argument" test "$(inner_cmd 1 | sed -n 3p)" = 'https://x.org/a b.git?x=1&y=2;z'
  check "script" test "$(inner_cmd 1 | head -n2 | tr '\n' ' ')" = "./start.sh web "
}

test_make_ssh_with_and_without_command() {
  mk ssh MACHINE=web
  check "interactive: no command argument" test "$(inner_cmd 1 | tr '\n' ' ')" = "./connect.sh web "
  rm "$MOCK_STATE"/docker-call-*
  mk ssh MACHINE=web 'CMD=tmux attach -t claude; echo "x"'
  check "command is one argument" test "$(inner_cmd 1 | sed -n 3p)" = 'tmux attach -t claude; echo "x"'
}

test_make_requires_machine() {
  for target in preflight up ssh down; do
    fails mk "$target"
    check "$target usage" has "Usage: make $target MACHINE=<name>" "$T/make-err"
  done
}

test_connect_uses_the_machine_config() {
  run setup.sh >/dev/null
  fails run connect.sh web >/dev/null 2>"$T/err"
  check "needs start first" has 'run ./start.sh web first' "$T/err"
  run start.sh web >/dev/null
  : > "$CALLS"
  run connect.sh web 'ls -la' </dev/null
  check "own config, pinned alias" has "^ssh -F $HOME/.ssh/config_hetzner_claude-web hetzner-claude-web ls -la$" "$CALLS"
}

test_make_without_target_shows_help_only() {
  make -s --no-print-directory -C "$ROOT" >"$T/help" 2>&1
  for target in help setup preflight up ssh down test lint docker-test; do
    check "$target described" has "^  $target +[^ ]" "$T/help"
  done
  check "nothing run" bash -c "! ls '$MOCK_STATE'/docker-call-* >/dev/null 2>&1"
}

test_every_make_target_has_a_description() {
  # targets defined in the makefiles vs. targets listed by help
  defined=$(grep -hoE '^[a-z-]+:' "$ROOT/Makefile" "$ROOT/tests/tests.mk" | tr -d : | sort -u)
  listed=$(make -s --no-print-directory -C "$ROOT" help | awk '/^  [a-z]/ {print $1}' | sort -u)
  check "help lists all targets" test "$defined" = "$listed"
}

test_make_mounts_symlinked_ssh_config_target() {
  mkdir -p "$HOME/.ssh" "$T/dotfiles"; echo 'Host x' > "$T/dotfiles/ssh_config"
  ln -sf "$T/dotfiles/ssh_config" "$HOME/.ssh/config"
  mk preflight MACHINE=web
  check "target mounted at the same path" bash -c "grep -A1 -x -- '-v' '$MOCK_STATE/docker-call-1' | grep -qxF '$T/dotfiles/ssh_config:$T/dotfiles/ssh_config'"
}
