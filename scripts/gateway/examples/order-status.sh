#!/usr/bin/env bash
# M8 executable curl example. Never enable shell tracing with credentials loaded.
set -euo pipefail
set +x
: "${CH_OPEN_BASE_URL:?Set the API origin}"
: "${CH_OPEN_APP_ID:?Set the application ID}"
: "${CH_OPEN_SECRET:?Load the issued secret from your secret manager}"
path=${CH_OPEN_PATH:-/open/v1/orders/${CH_OPEN_ORDER_ID:?Set the public order ID}/status}
# This curl example signs query-free GETs. Use the Go sample for canonical query.
[[ "$path" == /open/v1/* && "$path" != *'?'* && "$path" != *$'\n'* ]] || exit 1
timestamp=$(date +%s)
nonce=$(openssl rand -hex 16)
body_hash=$(printf '%s' '' | openssl dgst -sha256 -r | cut -d' ' -f1)
signature=$(printf 'GET\n%s\n\n%s\n%s\n%s' "$path" "$timestamp" "$nonce" "$body_hash" |
    openssl dgst -sha256 -hmac "$CH_OPEN_SECRET" -binary | openssl base64 -A)
curl --silent --show-error --fail --max-time 10 --path-as-is \
    "${CH_OPEN_BASE_URL%/}$path" \
    -H "X-App-Id: $CH_OPEN_APP_ID" -H "X-Timestamp: $timestamp" \
    -H "X-Nonce: $nonce" -H "X-Signature: $signature"
