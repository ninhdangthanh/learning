#!/usr/bin/env bash
set -euo pipefail

API_URL="${API_URL:-http://localhost:8080}"
REQUESTS="${REQUESTS:-80}"
EMAIL="load-$(date +%s)@example.com"
PASSWORD="password123"
IP="203.0.113.${RANDOM:0:2}"

BODY=$(printf '{"email":"%s","password":"%s"}' "${EMAIL}" "${PASSWORD}")

ACCESS=$(curl -sS -X POST "${API_URL}/auth/register" \
  -H "X-Forwarded-For: ${IP}" \
  -H 'Content-Type: application/json' \
  -d "${BODY}" |
  python3 -c 'import json,sys; print(json.load(sys.stdin)["tokens"]["access_token"])')

printf 'Firing %s requests at %s/api/notes\n\n' "${REQUESTS}" "${API_URL}"

allowed=0
blocked=0

for _ in $(seq 1 "${REQUESTS}"); do
  status=$(curl -sS -o /dev/null -w '%{http_code}' \
    "${API_URL}/api/notes" -H "X-Forwarded-For: ${IP}" -H "Authorization: Bearer ${ACCESS}")
  if [ "${status}" = "429" ]; then
    blocked=$((blocked + 1))
  else
    allowed=$((allowed + 1))
  fi
done

printf 'allowed: %s\nblocked (429): %s\n\n' "${allowed}" "${blocked}"

printf 'Headers on the next request:\n'
curl -sS -D - -o /dev/null "${API_URL}/api/notes" \
  -H "X-Forwarded-For: ${IP}" -H "Authorization: Bearer ${ACCESS}" | grep -i -E 'ratelimit|retry-after'
