#!/usr/bin/env sh
set -eu

relay_url="${RELAY_URL:-http://localhost:8080}"
secret="${RELAY_CHANNEL_DEMO_SECRET:-replace-with-at-least-16-random-characters}"
payload="${1:-{\"event\":\"ticket.created\",\"ticketId\":42}}"
idempotency_key="${IDEMPOTENCY_KEY:-demo-$(date +%s)}"

digest="$(
  printf '%s' "$payload" |
    openssl dgst -sha256 -hmac "$secret" -hex |
    awk '{print $2}'
)"

curl --fail-with-body --silent --show-error \
  --request POST \
  --header "Content-Type: application/json" \
  --header "Idempotency-Key: ${idempotency_key}" \
  --header "X-Webhook-Signature: sha256=${digest}" \
  --data "$payload" \
  "${relay_url}/webhooks/demo"
printf '\n'
