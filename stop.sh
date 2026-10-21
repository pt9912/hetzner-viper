#!/usr/bin/env bash
set -euo pipefail
MACHINE="${1:?Usage: ./stop.sh <machine> [--force]}"; export MACHINE
source "$(dirname "$0")/common.sh"
if ! hcloud server describe "$SERVER_NAME" >/dev/null 2>&1; then echo 'Server already absent'; exit 0; fi
IP=$(hcloud server ip "$SERVER_NAME")
FORCE=0; [[ "${2:-}" == --force ]] && FORCE=1
if (( FORCE )) || ! ssh -o BatchMode=yes -o ConnectTimeout=10 "$SSH_ALIAS" true 2>/dev/null; then
  (( FORCE )) || { echo 'SSH unreachable. Use ./stop.sh <machine> --force to delete without checks.' >&2; exit 1; }
  echo 'WARNING: --force skips the git checks; everything on the server is lost.' >&2
  SKIP=1
else SKIP=0
fi
(( SKIP )) || {
echo 'Checking git repositories and running sessions ...'
report=$(ssh "$SSH_ALIAS" bash -s "$WORKSPACE_DIR" <<'REMOTE'
set -euo pipefail
MP="$1"; unsaved=0
while IFS= read -r -d '' gitdir; do
  repo="${gitdir%/.git}"
  echo "== $repo =="
  dirty=$(git -C "$repo" status --short)
  [[ -z "$dirty" ]] || { echo "$dirty"; unsaved=1; }
  if unpushed=$(git -C "$repo" log --oneline '@{upstream}..HEAD' 2>/dev/null); then
    [[ -z "$unpushed" ]] || { echo "$unpushed"; unsaved=1; }
  else
    echo 'No upstream / cannot verify pushed commits'; unsaved=1
  fi
done < <(find "$MP" -name .git -prune -type d -print0)
echo '== tmux sessions =='
tmux ls 2>/dev/null || true
(( unsaved == 0 )) || echo 'UNSAVED WORK: uncommitted or unpushed changes exist and will be lost.'
REMOTE
)
echo "$report"
if grep -q '^UNSAVED WORK' <<<"$report"; then
  read -r -p 'Unsaved work will be lost. Type DISCARD to continue: ' discard
  [[ "$discard" == DISCARD ]] || { echo 'Cancelled: commit and push first'; exit 1; }
fi
}
printf '\nServer %s (%s) and ALL its data (including %s) will be DESTROYED.\n' "$SERVER_NAME" "$IP" "$WORKSPACE_DIR"
read -r -p 'Type DELETE to continue: ' confirm
[[ "$confirm" == DELETE ]] || { echo 'Cancelled'; exit 1; }
hcloud server delete "$SERVER_NAME"
./dns.sh delete "$IP" || echo 'WARNING: DNS removal failed; remove the stale record manually.' >&2
rm -f "$STATE/ip-$MACHINE"
echo "Server $SERVER_NAME deleted."
