# F3 Channels Implementation Plan

**Goal:** Implement `docs/tasks/F3-channels.md`, including its nine listed routes and ten acceptance checks.
**Architecture:** Keep existing credential notification contracts compatible. Put general transport contracts and rendering under `internal/kernel/notifier/channel`; persistence and application composition stay in bootstrap. Resolve recipient user references at delivery time; never persist destination addresses in delivery/outbox records.
**Tech Stack:** Existing Go, PostgreSQL/GORM, Gin, Sealer and standard-library transports; no new dependencies.
**Spec:** `docs/tasks/F3-channels.md`; `docs/design/admin-framework.md` section 3.

## Global Constraints
- Configuration encrypted with tenant/provider/kind AAD and returned through an explicit safe projection.
- Only recipient hash/domain in delivery records; outbox contains a user reference, never the address.
- No real provider calls, no F1 work, no changes to merged migrations or design conclusions.
- External calls outside database transactions; context cancellation and bounded timeouts.

### Task 1: Transport adapters
- [x] Create `internal/kernel/notifier/channel/{sender,config,smtp,http,aliyun,template}*.go` and focused tests.
- Contracts: `Sender.Channel() string`, `Kind() string`, `Send(context.Context, Message) error`, `Test(context.Context) error`; `NewSender(channel,kind string,Config) (Sender,error)`.
- `Message` includes `ID int64`, `Recipient,Subject,Body,Format string`, `Variables map[string]string`. `Config` is a string map; `test_recipient` is injected only in memory by management tests.
- SMTP config: `smtp_addr`, `username`, `password`, `from_address`, `from_name`, `tls_mode`; HTTPS transports: `endpoint`, `api_key`, `access_key_id`, `access_key_secret`, `region`, `from_address`, `sign_name`, `template_code`, `secret` as relevant.
- Validate provider/channel matching and required config; no provider URLs or credentials hardcoded. HTTPS required except loopback httptest endpoints. SMTP supports implicit TLS and STARTTLS, only loopback permits plaintext. Tests use fake local SMTP and httptest HTTP servers.
- Verify wire protocols (SendGrid JSON/auth; Aliyun RPC signing and provider failure codes; robot response error codes; signed generic webhook), cancellation, TLS and sanitized errors. Do not return endpoints, recipients or upstream bodies in errors.

### Task 2: Durable delivery and persistence
- [x] Add reversible migration 23 for the four F3 tables, global uniqueness and pending scan indexes; seed default templates.
- [x] Add kernel channel delivery types/service ports, restricted template rendering, safe provider views and encryption envelope.
- [x] Implement bootstrap stores for tenant/global provider selection, template override, atomic queue/claim/finish, recipient resolution and administrator inbox failures. Queue in the caller transaction; deliver outside it. Preserve outbox on unavailable channels and retry through a lease; avoid concurrent retry duplication.
- [x] Verify unconfigured/disabled channels, fallback attempts, administrator failure notifications, unknown variables, recipient privacy, AAD substitution and transaction rollback with deterministic unit/integration tests.

### Task 3: Management and composition
- [x] Implement provider list/create/update/delete/test, templates list/put, deliveries list/retry under `/api/v1/admin/notifications`; existing JWT actor plus system settings permission, audit and optional idempotency.
- [x] Limit tests to three per tenant/minute using a shared durable counter; tests use current actor contact rather than accepting arbitrary recipients.
- [x] Wire worker outbox polling and credential sends through configured providers while keeping existing legacy settings behavior.
- [x] Add HTTP tests for all routes, auth/tenant isolation, config redaction, rate limit, retry and idempotency. Put integration tests in the existing Admin CI group.

### Task 4: Verification and report
- [x] Run targeted tests, lint, full tests and `scripts/verify.ps1 -Integration -Migration`; verify migration chain/down/up and group guard.
- [x] Check new Go file/function size, placeholders, logs, diff and secret projections. Record commands and all ten acceptance items in the F3 task card, then stop and report.

## Decisions and progress
- Ruling: implement nine management routes — section 3 includes an explicit retry route; the prompt's count of eight omits this listed operation.
- Ruling: outbox stores user IDs and an encrypted request snapshot, with destinations resolved at send/retry time — satisfies privacy without losing retry capability.
- Preflight: Tasks 1/2 share Message/Config/Sender; Task 2/3 share service/store DTOs. Existing legacy Sender is kept separate to avoid breaking credential callers. Only Task 2 owns schema; only Task 3 owns application wiring. No contradictory values found.
- Regression: configured providers take precedence over legacy settings for the same channel, including when all configured providers are disabled. Verified by `TestAdminNotificationIntegrationCredentialUsesManagedProvider`.
- Verified local SMTP TLS and STARTTLS, six HTTP provider wire protocols, cancellation and sanitized failures. All twelve F3 integration tests pass. `make lint`, `make test`, and full `verify -Integration -Migration` pass; migration versions are 23/22/23 with dirty=false. The CI group guard reports `Source 391 = shards 390 + excluded 1`.
