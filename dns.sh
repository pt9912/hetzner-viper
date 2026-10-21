#!/usr/bin/env bash
set -euo pipefail
# Usage: MACHINE=<name> ./dns.sh set IP | delete [IP] | check
source "$(dirname "$0")/common.sh"
api="https://api.cloudflare.com/client/v4/zones/$CLOUDFLARE_ZONE_ID/dns_records"
# The token is passed as a header file (printf is a builtin), so it never appears in curl's argv.
cf() {
  local method="$1" url="$2" payload="${3:-}" response
  if [[ -n "$payload" ]]; then
    response=$(curl -fsS -X "$method" -H @<(printf 'Authorization: Bearer %s\n' "$CLOUDFLARE_API_TOKEN") -H 'Content-Type: application/json' --data "$payload" "$url") || return 1
  else
    response=$(curl -fsS -X "$method" -H @<(printf 'Authorization: Bearer %s\n' "$CLOUDFLARE_API_TOKEN") "$url") || return 1
  fi
  # explicit checks: set -e does not apply inside $(...), and jq -e on empty input differs between versions
  [[ -n "$response" ]] && jq -e '.success == true' <<<"$response" >/dev/null || { echo "Cloudflare API error: $response" >&2; return 1; }
  printf '%s\n' "$response"
}
records=$(cf GET "$api?type=A&name=$DNS_HOST&per_page=100") || { echo "Cloudflare lookup of $DNS_HOST failed" >&2; exit 1; }
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
 check)
   # read-only: zone matches DNS_DOMAIN, record free or owned by this setup
   zone=$(cf GET "https://api.cloudflare.com/client/v4/zones/$CLOUDFLARE_ZONE_ID") || { echo "FAIL  Cloudflare zone $CLOUDFLARE_ZONE_ID not readable with this token"; exit 1; }
   zone_name=$(jq -r '.result.name' <<<"$zone")
   if [[ "$DNS_DOMAIN" != "$zone_name" && "$DNS_DOMAIN" != *".$zone_name" ]]; then
     echo "FAIL  DNS_DOMAIN $DNS_DOMAIN is not in Cloudflare zone $zone_name"; exit 1
   fi
   echo "ok    Cloudflare zone $zone_name readable (edit permission is only seen when start.sh writes the record)"
   if [[ -z "$id" ]]; then
     echo "ok    $DNS_HOST: no record yet"
   elif [[ "$(jq -r '.result[0].comment // ""' <<<"$records")" == 'managed by hetzner-claude' ]]; then
     echo "info  $DNS_HOST -> $(jq -r '.result[0].content' <<<"$records") (managed by this setup)"
   else
     echo "FAIL  $DNS_HOST already has a record not managed by this setup"; exit 1
   fi
   ;;
 *) echo 'Usage: dns.sh set IP | delete [EXPECTED_IP] | check' >&2; exit 1;;
esac
