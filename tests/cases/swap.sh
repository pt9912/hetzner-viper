# shellcheck shell=bash
# Swap file on the server, opt-in with VIPER_SWAP (a factor of the RAM or a size).

swap_settings() { printf 'VIPER_SWAP=%s\n' "$1" > "$W/machines/web.env.local"; }

test_swap_is_off_by_default() {
  run setup.sh >/dev/null
  run start.sh web >"$T/out"
  check "nothing done" test ! -e "$MOCK_STATE/swap.log"
}

test_swap_factor_of_the_ram() {
  run setup.sh >/dev/null
  swap_settings 1
  run start.sh web >"$T/out"
  check "as large as the RAM (8 GB)" has 'fallocate -l 8192M ' "$MOCK_STATE/swap.log"
  check "private swap file" test "$(stat -c %a "$MOCK_STATE/remote-swapfile")" = 600
  check "formatted and enabled" bash -c "grep -q '^mkswap' '$MOCK_STATE/swap.log' && grep -q '^swapon .*remote-swapfile' '$MOCK_STATE/swap.log'"
  check "kept over reboots" has 'remote-swapfile none swap sw 0 0' "$MOCK_STATE/remote-fstab"
  check "little swapping" has 'vm.swappiness=10' "$MOCK_STATE/remote-sysctl.d/99-viper-swap.conf"
  check "announced" has '^swap: 8192 MB at .*RAM about 8 GB, VIPER_SWAP=1' "$T/out"
  : > "$MOCK_STATE/swap.log"
  swap_settings 0.5
  run start.sh web >"$T/again"
  check "idempotent" bash -c "! grep -q '^fallocate' '$MOCK_STATE/swap.log'"
  check "says so" has '^swap: .* already active' "$T/again"
  check "fstab once" count_is 'remote-swapfile' "$MOCK_STATE/remote-fstab" 1
}

test_swap_factor_half_and_size() {
  run setup.sh >/dev/null
  swap_settings 0.5
  run start.sh web >/dev/null
  check "half" has 'fallocate -l 4096M ' "$MOCK_STATE/swap.log"
  rm -f "$MOCK_STATE/swap-active" "$MOCK_STATE/remote-swapfile" "$MOCK_STATE/swap.log"
  swap_settings 3G
  run start.sh web >/dev/null
  check "size" has 'fallocate -l 3072M ' "$MOCK_STATE/swap.log"
}

test_swap_failure_is_not_fatal() {
  run setup.sh >/dev/null
  swap_settings 1
  MOCK_SWAP_FAIL=1 run start.sh web >"$T/out" 2>"$T/err"
  check "warning" has 'WARNING: swap could not be set up' "$T/err"
  check "still ready" has '^Ready: ssh hetzner-claude-web' "$T/out"
  check "no half-made file" test ! -e "$MOCK_STATE/remote-swapfile"
}

test_swap_needs_room_on_the_disk() {
  run setup.sh >/dev/null
  swap_settings 1
  MOCK_DISK_FREE=9000 run start.sh web >"$T/out" 2>"$T/err"
  check "reason" has 'swap: only 9000 MB free on the disk, 8192 MB needed' "$T/out"
  check "warning" has 'WARNING: swap could not be set up' "$T/err"
  check "nothing created" test ! -e "$MOCK_STATE/remote-swapfile"
}

test_swap_value_is_checked() {
  run setup.sh >/dev/null
  swap_settings 1
  run preflight.sh web >"$T/ok"
  check "factor" has "ok    swap file of 1 x the server's RAM" "$T/ok"
  swap_settings 4G
  run preflight.sh web >"$T/size"
  check "size" has 'ok    swap file of 4G on the server' "$T/size"
  for v in 5 0.01 big 4GB -1; do
    swap_settings "$v"
    fails run preflight.sh web >"$T/bad"
    check "rejected: $v" has "VIPER_SWAP must be 0, a factor" "$T/bad"
  done
}

test_check_reports_swap() {
  run setup.sh >/dev/null
  swap_settings 1
  run start.sh web >/dev/null
  run check.sh web >"$T/out"
  check "active" has 'ok    swap active \(8192 MB\)' "$T/out"
  rm "$MOCK_STATE/swap-active"
  fails run check.sh web >"$T/gone"
  check "not active" has 'FAIL  swap not active' "$T/gone"
}
