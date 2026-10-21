#!/usr/bin/env bash
set -euo pipefail
# Usage: MACHINE=<name> ./dns.sh set IP | delete [IP]
source "$(dirname "$0")/common.sh"
: "${CLOUDFLARE_ZONE_ID:?Set CLOUDFLARE_ZONE_ID (your DNS zone)}"
api="https://api.cloudflare.com/client/v4/zones/$CLOUDFLARE_ZONE_ID/dns_records"
cf() {
  local method="$1" url="$2" payload="${3:-}" response
  if [[ -n "$payload" ]]; then
    response=$(curl -fsS -X "$method" -H "Authorization: Bearer $CLOUDFLARE_API_TOKEN" -H 'Content-Type: application/json' --data "$payload" "$url")
  else
    response=$(curl -fsS -X "$method" -H "Authorization: Bearer $CLOUDFLARE_API_TOKEN" "$url")
  fi
  jq -e '.success == true' <<<"$response" >/dev/null || { echo "$response" | jq . >&2; return 1; }
  printf '%s\n' "$response"
}
records=$(cf GET "$api?type=A&name=$DNS_HOST&per_page=100")
count=$(jq '.result|length' <<<"$records")
(( count <= 1 )) || { echo 'Multiple A records; refusing to modify' >&2; exit 1; }
id=$(jq -r '.result[0].id // empty' <<<"$records")
case "${1:-}" in
 set)
   ip="${2:?Usage: dns.sh set IPv4}"
   [[ "$ip" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || exit 1
   payload=$(jq -n --arg name "$DNS_HOST" --arg ip "$ip" '{type:"A",name:$name,content:$ip,ttl:60,proxied:false,comment:"managed by hetzner-claude"}')
   if [[ -n "$id" ]]; then
     comment=$(jq -r '.result[0].comment // ""' <<<"$records")
     [[ "$comment" == 'managed by hetzner-claude' ]] || { echo 'Existing DNS record is not managed by this setup; refusing overwrite' >&2; exit 1; }
     cf PUT "$api/$id" "$payload" >/dev/null
   else cf POST "$api" "$payload" >/dev/null; fi
   echo "$DNS_HOST -> $ip"
   ;;
 delete)
   if [[ -n "$id" ]]; then
     comment=$(jq -r '.result[0].comment // ""' <<<"$records")
     [[ "$comment" == 'managed by hetzner-claude' ]] || { echo 'DNS record not owned by setup; skipping' >&2; exit 1; }
     if [[ -n "${2:-}" ]]; then
       old=$(jq -r '.result[0].content' <<<"$records")
       [[ "$old" == "$2" ]] || { echo 'DNS IP changed; refusing deletion' >&2; exit 1; }
     fi
     cf DELETE "$api/$id" >/dev/null
   fi
   echo 'DNS record removed (if present)'
   ;;
 *) echo 'Usage: dns.sh set IP | delete [EXPECTED_IP]' >&2; exit 1;;
esac
