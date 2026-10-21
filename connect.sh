#!/usr/bin/env bash
# Shell or one command on a machine, with the machine's own ssh config (pinned host key).
# Usage: ./connect.sh <machine> [command]
set -euo pipefail
MACHINE="${1:?Usage: ./connect.sh <machine> [command]}"; export MACHINE
source "$(dirname "$0")/common.sh"
[[ -f "$SSH_CFG" ]] || { echo "No SSH config for $SERVER_NAME: run ./start.sh $MACHINE first" >&2; exit 1; }
# the command (if any) is passed as one string; the remote shell parses it, as with plain ssh.
# With a command ssh allocates no terminal; request one when we have one (tmux attach, top, ...).
tty=()
(( $# > 1 )) && [[ -t 0 && -t 1 ]] && tty=(-t)
exec ssh "${tty[@]}" -F "$SSH_CFG" "$SSH_ALIAS" "${@:2}"
