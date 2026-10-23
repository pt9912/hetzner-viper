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

test_make_keeps_dollar_signs_in_cmd_and_repo() {
  mk ssh MACHINE=web 'CMD=echo "$HOME" rc=$? $(id -u)'
  check "cmd verbatim" test "$(inner_cmd 1 | sed -n 3p)" = 'echo "$HOME" rc=$? $(id -u)'
  rm "$MOCK_STATE"/docker-call-*
  mk up MACHINE=web 'REPO=https://x.org/a$b.git'
  check "repo verbatim" test "$(inner_cmd 1 | sed -n 3p)" = 'https://x.org/a$b.git'
}

test_build_uses_docker_hub_when_it_is_reachable() {
  mk build
  check "no base image given" bash -c "! grep -q BASE_IMAGE '$MOCK_STATE/docker-call-0'"
}

test_build_falls_back_to_the_mirror_when_docker_hub_is_unreachable() {
  MOCK_HUB_DOWN=1 mk build
  check "mirror used" bash -c "grep -qx 'BASE_IMAGE=mirror.gcr.io/library/debian:bookworm-slim' '$MOCK_STATE/docker-call-0'"
  check "says so" has 'Docker Hub is not reachable; building from mirror\.gcr\.io' "$T/make-err"
}

test_build_honours_an_explicit_base_image_without_probing() {
  MOCK_HUB_DOWN=1 mk build BASE_IMAGE=registry.example.invalid/debian:12
  check "given source used" bash -c "grep -qx 'BASE_IMAGE=registry.example.invalid/debian:12' '$MOCK_STATE/docker-call-0'"
  check "no probe" bash -c "! grep -q registry-1.docker.io '$CALLS'"
}

test_build_has_no_user_in_the_image() {
  mk build
  check "no uid baked in" bash -c "! grep -q 'USER_' '$MOCK_STATE/docker-call-0'"
}

test_make_runs_with_a_generated_passwd_for_the_host_user() {
  mk preflight MACHINE=web
  check "passwd mounted read-only, from the state directory" bash -c "grep -qxE '$HOME/.local/state/hetzner-claude/passwd\\.[0-9]+:/etc/passwd:ro' '$MOCK_STATE/docker-call-1'"
  check "removed after the run" bash -c "! ls '$HOME'/.local/state/hetzner-claude/passwd.* >/dev/null 2>&1"
  check "user is the host user" bash -c "grep -qx -- \"$(id -u):$(id -g)\" '$MOCK_STATE/docker-call-1'"
}

test_on_a_release_the_published_image_is_pulled_and_used() {
  MOCK_NO_IMAGE=1 mk build RELEASE=v1.2.3
  check "looked for it locally" bash -c "grep -qx inspect '$MOCK_STATE/docker-call-0'"
  check "pulled" test "$(tail -n1 "$MOCK_STATE/docker-call-1")" = ghcr.io/pt9912/hetzner-viper:v1.2.3
  check "not built" bash -c "! grep -qx build '$MOCK_STATE'/docker-call-*"
  rm "$MOCK_STATE"/docker-call-*
  mk preflight MACHINE=web RELEASE=v1.2.3
  check "runs the release image" bash -c "grep -qx ghcr.io/pt9912/hetzner-viper:v1.2.3 '$MOCK_STATE/docker-call-1'"
}

test_on_a_release_an_image_already_there_is_not_pulled_again() {
  mk build RELEASE=v1.2.3
  check "only the lookup" bash -c "test \$(ls '$MOCK_STATE'/docker-call-* | wc -l) = 1"
}

test_release_with_an_unavailable_image_says_how_to_build_locally() {
  fails env MOCK_NO_IMAGE=1 MOCK_DOCKER_PULL_FAIL=1 make -s --no-print-directory -C "$ROOT" build RELEASE=v1.2.3 2>"$T/make-err"
  check "hint" has 'pass RELEASE= to every make call' "$T/make-err"
}

# repo_with_tags <dir> <tag>...: a copy of the Makefile in a git repository whose HEAD has these tags
repo_with_tags() {
  local d="$1"; shift
  mkdir -p "$d/tests"; cp "$ROOT/Makefile" "$d/"; cp "$ROOT/tests/tests.mk" "$d/tests/"
  git -C "$d" init -q; git -C "$d" add .
  git -C "$d" -c user.name=t -c user.email=t@t commit -q -m x
  for t in "$@"; do git -C "$d" tag "$t"; done
}
# build_in <dir>: make build there with the release detection switched on, image not present
build_in() {
  env -u RELEASE MOCK_NO_IMAGE=1 make -s --no-print-directory -C "$1" build >/dev/null 2>&1
}

test_release_is_detected_from_a_version_tag() {
  repo_with_tags "$T/r" v1.2.3
  build_in "$T/r"
  check "pulled the tag's image" test "$(tail -n1 "$MOCK_STATE/docker-call-1")" = ghcr.io/pt9912/hetzner-viper:v1.2.3
}

test_other_tags_do_not_select_a_release_image() {
  repo_with_tags "$T/r" wip release/2 v1+build
  build_in "$T/r"
  check "built locally" bash -c "grep -qx build '$MOCK_STATE/docker-call-0'"
}

test_an_archive_inside_another_repository_does_not_pick_up_its_tags() {
  repo_with_tags "$T/r" v1.2.3
  mkdir "$T/r/unpacked"; cp "$ROOT/Makefile" "$T/r/unpacked/"; mkdir "$T/r/unpacked/tests"; cp "$ROOT/tests/tests.mk" "$T/r/unpacked/tests/"
  build_in "$T/r/unpacked"
  check "built locally" bash -c "grep -qx build '$MOCK_STATE/docker-call-0'"
}
