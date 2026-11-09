# S3 process smoke test

Run from the repository with Windows PowerShell 5.1 and the existing Go toolchain:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/smoke.ps1 -RedisAddr 127.0.0.1:16379
```

`DatabaseUrl` defaults to `TEST_DATABASE_URL` in the process environment or the
repository `.env`. `RedisAddr`, Redis password and the development Redis DB read
`CH_REDIS_ADDR`, `CH_REDIS_PASSWORD` and `CH_REDIS_DB` the same way. The fallback
Redis address is `127.0.0.1:6379`. These must be real PostgreSQL and Redis services.
The database login needs migration/extension/schema permissions. No browser,
psql, redis-cli, Docker CLI, object storage or new Go dependency is required.
The 15 steps do not upload/download objects; export/storage is outside S3.
Step 15 also requires bash, curl and OpenSSL (Git for Windows supplies them).

The first step builds the existing API, worker and CLI plus the small fixture
helper, using the normal external Go caches. Keep those caches outside the
repository. It creates an empty random `s3_smoke_<uuid>` schema, runs migration
up/down/up, and checks its version against the embedded latest migration.
Both URL and keyword PostgreSQL DSNs are supported. Application search_path
contains only this schema, without a fallback into development tables.

Redis uses an atomically reserved, initially empty logical DB (1..15), excluding
the configured development DB. A unique ownership marker guards deletion. No
FLUSHDB/FLUSHALL is used. A Redis server with no available logical DB fails step
1 explicitly; use a dedicated Redis server in that case.

Every HTTP response records its status and request_id under a safe operation
label. Bodies, secret URLs, cookies, passwords and contact fields are never
printed. A fixture TLS proxy listens on a random loopback port; its certificate
is trusted only by the helper HTTP client. This exercises the real API with
Secure/HttpOnly cookies, the actual cookie path, and an HTTPS same-origin inquiry.
No machine-wide certificate or TLS settings change.

Every step has a 30-second budget; the overall budget is 300 seconds, including
builds and a reserved cleanup window. Polling is bounded at 500ms intervals.
The first failing step determines the exit code (1..15); subsequent business
steps are explicitly SKIPPED with that reason. Step 14 always runs cleanup last.
Execution order is 1..13, 15, then cleanup 14, preserving existing step numbers.
Normal success and failure terminate owned process trees using Windows Job
Objects, remove the schema and Redis keys, and verify zero remaining resources.

`-KeepArtifacts` retains sanitized process logs, `http.json`, `summary.json`,
temporary executables and isolated data **only on failure**; processes still
stop. The output identifies the retained temporary directory, schema and Redis
DB for inspection/manual removal. This is an explicit retention request, not
a normal cleanup result. A successful run cleans artifacts even with this flag.

To prove that a broken behavior fails at the correct step without changing the
product, omit the actual revoke call deliberately:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/smoke.ps1 -RedisAddr 127.0.0.1:16379 -Fault SkipRevoke
# Step 13 (Revoke link) failed: expected HTTP 404, got 200; exit 13; cleanup runs.
```

The following task-card differences were explicitly approved for S3:

- Worker has no HTTP port. Its process must stay alive, and Inspector.Servers()
  must report its actual PID as active with exactly billing/notify/refresh/sync.
  This is a PASS assertion, not SKIPPED. The missing endpoint is backlog #4.
- API `/readyz` needs JWT. Step 2 bootstraps seed/login before checking status=ok
  and db=ok/redis=ok; step 3 independently repeats seed and verifies one admin
  plus a fresh successful login.
- Revoked anonymous access must immediately return the existing contract's
  HTTP 404 and business code 70102, including with an admitted cached browser.

Other precise observations: normal mock product data creates two unmerged
listings, one listing SKU and zero SPUs; mock order data creates two orders.
Step 7 creates a manual SPU and merges both listings, verifying SKU count 1 and
min/max price 1234. Step 8 assigns 777 as the exclusive price. Inquiry submission
honors the existing two-second admission dwell rule. Step 12 asserts 403/20108
before reauthentication and exact cleartext plus success/denial audits after it.

Implementation/verification checklist:

- [x] Isolated infrastructure, captured child processes and deadlines.
- [x] Fifteen individually numbered behavior assertions and a final summary.
- [x] Real PostgreSQL/Redis/API/worker run through sharing and inquiry.
- [x] Final fault-injection and two consecutive successful acceptance runs.
- [x] Final repository lint/tests and independent resource reconciliation.

Acceptance evidence (2026-11-09, Windows PowerShell 5.1.26100.9711):

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/smoke.ps1 -RedisAddr 127.0.0.1:16379
```

Both consecutive runs used a new empty schema and returned exit 0. Durations
include builds, migration up/down/up, all business assertions and cleanup.

| Step | Assertion | Result, both runs | Run 1 seconds | Run 2 seconds |
|---|---|---|---|---|
| 1 | Dependencies and latest clean migration | PASS | 21.34 | 21.26 |
| 2 | API readyz and live Inspector queue set | PASS | 2.08 | 2.04 |
| 3 | Idempotent seed and fresh login | PASS | 0.92 | 0.90 |
| 4 | Shop and decryptable credential | PASS | 0.64 | 0.59 |
| 5 | Product and order runs succeed | PASS | 2.92 | 2.80 |
| 6 | Exact fixture rows and identities | PASS | 0.17 | 0.13 |
| 7 | Two listings merged with recalculated totals | PASS | 0.70 | 0.60 |
| 8 | Customer, price list and exclusive price | PASS | 0.62 | 0.51 |
| 9 | Password, secret URL and expiry | PASS | 0.53 | 0.54 |
| 10 | Password admission and exclusive price | PASS | 0.70 | 0.73 |
| 11 | Inquiry ciphertext and inbox notification | PASS | 2.35 | 2.40 |
| 12 | Reauthentication, exact plaintext and audits | PASS | 0.80 | 0.77 |
| 13 | Cached browser denied immediately after revoke | PASS | 0.45 | 0.50 |
| 14 | Owned processes, schema, Redis keys and artifacts removed | PASS | 0.44 | 0.45 |
| Total | Including harness initialization | PASS | 35.10 | 34.66 |

Final `-Fault SkipRevoke` run: step 13 failed with expected HTTP 404, actual 200,
request_id=36867f78387c1ac6135d9b2aace97177; the actual native exit code was
verified as 13. Step 14 passed and total duration was 32.55 seconds.

Every run reported remaining_schemas=0 and remaining_redis_keys=0. A separate
read-only PostgreSQL catalog query after the fault run found zero schemas with
the s3_smoke_ prefix; an independent OS check found zero owned smoke processes.
Artifact removal is part of step 14, with bounded retries for transient Windows
file locks and a path check before recursive deletion.

`scripts/verify.ps1` ran the Makefile-equivalent `go vet ./...`,
`golangci-lint run` and `go test ./...`: final line `all checks passed`.
The helper's isolation tests cover URL/keyword DSNs, unsafe schema names and
Redis ownership/development-data protection. ASCII, PowerShell parser, gofmt,
file <=400 lines, function <=80 lines and git diff whitespace checks passed.
No product endpoints, schema semantics or third-party dependencies changed.

M8c extension (2026-11-10): step 15 uses management JWT to register the existing
order.status capability, create an application, grant its route and one actual
synced order. An independent Go signing client calls the real open stack; both
the standalone Go example and the bash curl example must also return 200.
The first response's server call_id must have one succeeded durable log and one
meter event; authenticated usage must then report exactly three successful
logical calls and no processing calls. Neither example receives a management
JWT. Secrets travel only over captured pipes/environment and never enter saved
HTTP evidence.

Optional `-GatewayDrill Rotation|Revocation|Quota` adds the corresponding real
drill to step 15. The three standalone entry points and expected PASS/FAIL
outputs are documented in [the M8 operations manual](../../docs/gateway/operations.md).
All runs retain the existing isolated schema/Redis and unconditional cleanup.
Current acceptance evidence is in [M8c acceptance](../../docs/milestones/M8c-acceptance.md).
