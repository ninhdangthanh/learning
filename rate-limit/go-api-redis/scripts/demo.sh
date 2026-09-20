#!/usr/bin/env bash
set -euo pipefail

API_URL="${API_URL:-http://localhost:8080}"
STAMP="$(date +%s)"
PASSWORD="password123"
IP="198.51.100.$((RANDOM % 200 + 1))"

credentials() {
  printf '{"email":"%s","password":"%s"}' "$1" "${PASSWORD}"
}

json() {
  python3 -c '
import json, sys

data = json.load(sys.stdin)
for key in sys.argv[1].split("."):
    data = data[key]
print(data)
' "$1"
}

call() {
  curl -sS -H "X-Forwarded-For: ${IP}" -H 'Content-Type: application/json' "$@"
}

status_of() {
  curl -sS -o /dev/null -w '%{http_code}' \
    -H "X-Forwarded-For: ${IP}" -H 'Content-Type: application/json' "$@"
}

limit_headers() {
  call -D - -o /dev/null "$@" | grep -i -E 'ratelimit-policy|ratelimit-remaining|retry-after'
}

step() {
  printf '\n\033[1;36m==> %s\033[0m\n' "$1"
}

step "Health check"
call "${API_URL}/healthz"
echo

step "Register demo-${STAMP}@example.com from ${IP}"
SESSION=$(call -X POST "${API_URL}/auth/register" -d "$(credentials "demo-${STAMP}@example.com")")
echo "${SESSION}"
ACCESS=$(printf '%s' "${SESSION}" | json tokens.access_token)
REFRESH=$(printf '%s' "${SESSION}" | json tokens.refresh_token)

step "Create a note"
call -X POST "${API_URL}/api/notes" \
  -H "Authorization: Bearer ${ACCESS}" \
  -d '{"title":"Redis only","body":"No database in this project"}'
echo

step "List notes"
call "${API_URL}/api/notes" -H "Authorization: Bearer ${ACCESS}"
echo

step "Rotate the refresh token"
ROTATED=$(call -X POST "${API_URL}/auth/refresh" -d "{\"refresh_token\":\"${REFRESH}\"}")
echo "${ROTATED}"
ROTATED_REFRESH=$(printf '%s' "${ROTATED}" | json refresh_token)

step "Replay the old refresh token (expect token_reused)"
call -X POST "${API_URL}/auth/refresh" -d "{\"refresh_token\":\"${REFRESH}\"}"
echo

step "Every session is revoked after reuse detection (expect invalid_refresh_token)"
call -X POST "${API_URL}/auth/refresh" -d "{\"refresh_token\":\"${ROTATED_REFRESH}\"}"
echo

step "Fixed window on /auth/register: 5 per hour per IP"
for i in $(seq 1 7); do
  body=$(credentials "fw-${i}-${STAMP}@example.com")
  printf '%s ' "$(status_of -X POST "${API_URL}/auth/register" -d "${body}")"
done
echo
limit_headers -X POST "${API_URL}/auth/register" -d "$(credentials "blocked-${STAMP}@example.com")"

step "Token bucket on /auth/login: burst 10, then one token every 5s"
login_body=$(credentials "nobody-${STAMP}@example.com")
for _ in $(seq 1 13); do
  printf '%s ' "$(status_of -X POST "${API_URL}/auth/login" -d "${login_body}")"
done
echo
limit_headers -X POST "${API_URL}/auth/login" -d "${login_body}"

step "Refresh keeps its own bucket, so a login flood cannot lock it out"
limit_headers -X POST "${API_URL}/auth/refresh" -d '{"refresh_token":"not-a-real-token"}'

printf '\nRun "make load-test" to watch the sliding window on /api/*.\n'
