set -uo pipefail
export LC_ALL=C
spec="$1"
f="${VIPER_SWAPFILE:-/swapfile}"; fstab="${VIPER_FSTAB:-/etc/fstab}"; sysctl_d="${VIPER_SYSCTL_D:-/etc/sysctl.d}"
# the kernel reports a little less than the nominal RAM (7.6 GB for 8 GB): round up
mem_gb=$(awk '/^MemTotal:/ {g = $2 / 1048576; printf "%d", g == int(g) ? g : int(g) + 1}' "${VIPER_MEMINFO:-/proc/meminfo}")
case "$spec" in
  *G) mb=$(( ${spec%G} * 1024 )) ;;
  *M) mb=${spec%M} ;;
  *) mb=$(awk -v g="$mem_gb" -v x="$spec" 'BEGIN {printf "%d", g * 1024 * x}') ;;
esac
(( mb >= 256 )) || mb=256
if swapon --show=NAME --noheadings 2>/dev/null | grep -qxF "$f"; then
  echo "swap: $f already active ($(swapon --show=SIZE --noheadings 2>/dev/null | head -n1 | tr -d ' '))"; exit 0
fi
avail=$(df -Pm "$(dirname "$f")" | awk 'NR == 2 {print $4}')
(( avail > mb + 2048 )) || { echo "swap: only ${avail} MB free on the disk, ${mb} MB needed (plus 2 GB left over)"; exit 1; }
if [ ! -e "$f" ]; then
  sudo fallocate -l "${mb}M" "$f" && sudo chmod 600 "$f" && sudo mkswap "$f" >/dev/null || { sudo rm -f "$f"; echo "swap: creating $f failed"; exit 1; }
fi
sudo swapon "$f" || { echo "swap: swapon $f failed"; exit 1; }
grep -qs "^$f " "$fstab" || echo "$f none swap sw 0 0" | sudo tee -a "$fstab" >/dev/null
# little swapping: it is a safety net against the OOM killer, not extra memory
echo 'vm.swappiness=10' | sudo tee "$sysctl_d/99-viper-swap.conf" >/dev/null
sudo sysctl -q vm.swappiness=10
echo "swap: ${mb} MB at $f (RAM about ${mem_gb} GB, VIPER_SWAP=$spec)"
