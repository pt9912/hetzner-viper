#!/usr/bin/env bash
# Read-only checks before start.sh: settings, local setup, Hetzner, Cloudflare, GitHub.
# Creates and changes nothing. start.sh runs it first.
# Usage: ./preflight.sh <machine> [repo-url]
set -euo pipefail
MACHINE="${1:?Usage: ./preflight.sh <machine> [repo-url]}"; export MACHINE
REPO_URL_ARG="${2:-}"
# settings and local tools; exits listing every missing setting
source "$(dirname "$0")/common.sh"
[[ -z "$REPO_URL_ARG" ]] || REPO_URL="$REPO_URL_ARG"

failures=0
ok()   { echo "ok    $*"; }
bad()  { echo "FAIL  $*"; failures=$((failures + 1)); }
info() { echo "info  $*"; }
warn() { echo "WARN  $*"; }

echo "== settings and local setup ($SERVER_NAME)"
ok "settings complete: $SERVER_TYPE in $SERVER_LOCATION, $SERVER_IMAGE, DNS $DNS_HOST"
CA="$HOME/.ssh/hetzner_claude_host_ca"
if [[ -f "$SSH_KEY" && -f "$SSH_KEY.pub" && -f "$CA" ]]; then
  ok "login key $SSH_KEY and host CA present"
else
  bad "login key or host CA missing: Run ./setup.sh first"
fi
if grep -Fq "@cert-authority *.$DNS_DOMAIN " "$HOME/.ssh/known_hosts_hetzner_claude_ca" 2>/dev/null; then
  ok "host CA trusted for *.$DNS_DOMAIN"
else
  bad "host CA trust for *.$DNS_DOMAIN missing (new or changed DNS_DOMAIN?): run ./setup.sh"
fi
for var in GIT_DEPLOY_KEY_FILE IPAD_SSH_PUBLIC_KEY_FILE; do
  f="${!var:-}"; f="${f/#\~/$HOME}"
  [[ -z "$f" ]] && continue
  if [[ -r "$f" ]]; then ok "$var readable"; else bad "$var: File not readable: $f (in the container only ~/.ssh is visible)"; fi
done
# config files hold API tokens: nobody but the owner should read them
for f in .env.local "machines/$MACHINE.env.local"; do
  [[ -f "$f" ]] || continue
  if [[ -n "$(find "$f" -perm /077 2>/dev/null)" ]]; then
    warn "$f is readable by other users; it may hold tokens: chmod 600 $f"
  fi
done
cidr_ok=1
IFS=',' read -r -a cidrs <<<"${SSH_ALLOWED_CIDRS:-0.0.0.0/0,::/0}"
for c in "${cidrs[@]}"; do
  [[ "$c" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}/[0-9]{1,2}$ || "$c" =~ ^[0-9a-fA-F:]+/[0-9]{1,3}$ ]] || { bad "SSH_ALLOWED_CIDRS: not a network: $c"; cidr_ok=0; }
done
(( cidr_ok )) && ok "SSH_ALLOWED_CIDRS: ${SSH_ALLOWED_CIDRS:-0.0.0.0/0,::/0}"

echo "== Hetzner Cloud"
if ! err=$(hcloud server list -o noheader -o columns=name 2>&1 >/dev/null); then
  bad "Hetzner API not usable (token, project, network): $err"
else
  ok "Hetzner token can read the project (write access is only seen when start.sh creates something)"
  if ! st=$(hcloud server-type describe "$SERVER_TYPE" -o json 2>/dev/null); then
    bad "server type $SERVER_TYPE does not exist (see: hcloud server-type list)"
  else
    arch=$(jq -r '.architecture // "x86"' <<<"$st")
    type_id=$(jq '.id' <<<"$st")
    ok "server type $SERVER_TYPE exists ($(jq -r '"\(.cores) vCPU, \(.memory) GB RAM, \(.disk) GB disk"' <<<"$st")), architecture $arch"
    if hcloud location describe "$SERVER_LOCATION" >/dev/null 2>&1; then
      ok "location $SERVER_LOCATION exists"
      # availability per location comes with the server type (the datacenter API was removed)
      loc=$(jq -c --arg loc "$SERVER_LOCATION" '[.locations[]? | select(.name == $loc)][0] // empty' <<<"$st")
      offered=$(jq -r '[.locations[]? | select(.available) | .name] | join(", ")' <<<"$st")
      if [[ -z "$loc" ]]; then
        bad "$SERVER_TYPE is not offered in $SERVER_LOCATION (available in: ${offered:-none}); set SERVER_LOCATION or SERVER_TYPE, e.g. in machines/$MACHINE.env.local"
      elif [[ "$(jq -r '.available' <<<"$loc")" != true ]]; then
        bad "$SERVER_TYPE cannot currently be ordered in $SERVER_LOCATION (available in: ${offered:-none}); set SERVER_LOCATION or SERVER_TYPE, e.g. in machines/$MACHINE.env.local"
      else
        ok "$SERVER_TYPE can currently be ordered in $SERVER_LOCATION"
        dep=$(jq -r '.deprecation.unavailable_after // empty' <<<"$loc")
        [[ -z "$dep" ]] || warn "$SERVER_TYPE is deprecated in $SERVER_LOCATION and unavailable after $dep"
      fi
    else
      bad "location $SERVER_LOCATION does not exist (see: hcloud location list)"
    fi
    if hcloud image describe --architecture "$arch" "$SERVER_IMAGE" >/dev/null 2>&1; then
      ok "image $SERVER_IMAGE exists for $arch"
    else
      bad "image $SERVER_IMAGE does not exist for $arch (see: hcloud image list --type system)"
    fi
  fi
  fingerprint=$(ssh-keygen -E md5 -lf "$SSH_KEY.pub" 2>/dev/null | awk '{print $2}' | sed 's/^MD5://' || true)
  keys=$(hcloud ssh-key list -o noheader -o columns=fingerprint,name)
  existing=$(awk -v fp="$fingerprint" '{ f = $1; sub(/^[^ ]+ +/, ""); sub(/ +$/, ""); if (fp != "" && tolower(f) == tolower(fp)) { print; exit } }' <<<"$keys")
  if [[ -n "$existing" ]]; then
    ok "login key already in the project as \"$existing\" (reused)"
  elif awk -v n="$KEY_NAME" '{ sub(/^[^ ]+ +/, ""); sub(/ +$/, "") } $0 == n { found = 1 } END { exit !found }' <<<"$keys"; then
    bad "SSH key $KEY_NAME in Hetzner belongs to a different public key than $SSH_KEY.pub"
  else
    info "login key will be uploaded as $KEY_NAME"
  fi
  if hcloud_has server "$SERVER_NAME"; then info "server $SERVER_NAME exists; start.sh reuses it"; else info "server $SERVER_NAME will be created"; fi
fi

echo "== Cloudflare DNS"
./dns.sh check || failures=$((failures + 1))

echo "== GitHub and repository"
if [[ -n "${VIPER_GH_TOKEN:-}" ]]; then
  gh_host="${VIPER_GH_HOST:-github.com}"
  api="https://api.github.com"; [[ "$gh_host" == github.com ]] || api="https://$gh_host/api/v3"
  headers=$(mktemp); trap 'rm -f "$headers"' EXIT
  # token as header file: it never appears in curl's argv
  if user=$(curl -fsS -D "$headers" -H @<(printf 'Authorization: Bearer %s\n' "$VIPER_GH_TOKEN") "$api/user" 2>&1); then
    ok "VIPER_GH_TOKEN valid for $(jq -r '.login' <<<"$user") on $gh_host"
    scopes=$(awk -F': ' 'tolower($1) == "x-oauth-scopes" {gsub("\r", "", $2); print $2}' "$headers")
    if [[ -n "$scopes" ]]; then
      for s in repo read:org; do
        [[ ",${scopes// /}," == *",$s,"* ]] || bad "classic token lacks scope $s (has: $scopes); gh auth login will reject it"
      done
    fi
    if [[ -n "${REPO_URL:-}" ]] && [[ "$REPO_URL" =~ ^(https://$gh_host/|git@$gh_host:)([^/]+)/([^/]+)$ ]]; then
      repo="${BASH_REMATCH[2]}/${BASH_REMATCH[3]%.git}"
      if curl -fsS -o /dev/null -H @<(printf 'Authorization: Bearer %s\n' "$VIPER_GH_TOKEN") "$api/repos/$repo" 2>/dev/null; then
        ok "token can access $repo"
      else
        bad "token cannot access $repo (fine-grained token without this repository?)"
      fi
    fi
  else
    bad "VIPER_GH_TOKEN rejected by $gh_host: $user"
  fi
else
  info "no VIPER_GH_TOKEN: gh is not logged in on the server"
fi
if [[ -z "${REPO_URL:-}" ]]; then
  info "no repository to clone"
elif [[ "$REPO_URL" =~ ^(ssh://|[^/]+@[^/]+:) ]]; then
  info "SSH repository $REPO_URL: access is only checked by the clone on the server"
elif [[ -n "${VIPER_GH_TOKEN:-}" && "$REPO_URL" == "https://${VIPER_GH_HOST:-github.com}/"* ]]; then
  : # checked through the GitHub API above
elif command -v git >/dev/null; then
  if GIT_TERMINAL_PROMPT=0 git ls-remote "$REPO_URL" >/dev/null 2>&1; then
    ok "repository $REPO_URL reachable"
  else
    bad "repository $REPO_URL not reachable without credentials (private? set VIPER_GH_TOKEN or GIT_DEPLOY_KEY_FILE)"
  fi
else
  info "git not installed locally: repository not checked"
fi

echo
if (( failures > 0 )); then
  echo "Preflight: $failures problem(s); nothing was created."
  exit 1
fi
echo 'Preflight OK.'
