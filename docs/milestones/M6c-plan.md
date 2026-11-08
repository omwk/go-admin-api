# M6c Implementation Plan

**Goal:** Complete REQ-SHR-007/008 and ADR-020 §5/6/9 with encrypted inquiry intake, controlled disclosure, retention, lifecycle revocation and public ingress isolation.
**Architecture:** Extend the existing share service and bootstrap adapters. Reuse the M5c Sealer/BlindIndexer and live session reauthentication, current customer owner scope, PostgreSQL transactions and inbox/outbox. No new dependencies or changes to catalog/order/crypto contracts.
**Tech Stack:** Existing Go, Gin, PostgreSQL, Redis, AES-GCM, HMAC.
**Spec:** M6 task instruction; ADR-020 §5/6/9; M6-review Q5/Q6/§7.3.

## Constraints

- Work only on `feat/M6c-inquiry-hardening`; finish and report without M7, merge or tag.
- New migration only; retain every existing regression assertion.
- Anonymous submission returns only `201` and `submitted=true`; failures use the existing safe message.
- Field AAD is `tenant:inquiry:id:field:revision`; store no inquiry plaintext, raw IP/UA or token in logs, audits, inbox, outbox or idempotency.
- Disclosure requires `trade:inquiry:pii:read`, current scope, session age strictly below five minutes and a committed audit.
- Visits retain only time/result/link reference for 90 days; no visitor or product profile.
- Inquiry PII expires at submission +180d or conversion +30d; customer contact PII expires at termination +180d. Clear ciphertext, not merely soft-delete.
- New Go files <=400 lines; functions <=80 lines; no new dependencies or repository-local Go caches.
- Approved option B (2026-11-09): migration 14 may delete only explicitly approved share safety event/resource pairs after three database-clock years; UPDATE/TRUNCATE and other audit deletion remain denied. Down restores the original full append-only trigger.

## Task 1: Encrypted inquiry intake and management

Files: `internal/modules/share/inquiry*.go`, `internal/bootstrap/share_inquiry*.go`, `internal/modules/identity/permission/registry.go`, `internal/bootstrap/scope.go`, `api/openapi/share-inquiry.yaml`.

Interfaces: `LinkService.SubmitInquiry(ctx, tenant, token, ip, cookie, key, input) error`; `InquiryService` uses an `InquiryStore` with live authorization, transaction, masked list, scoped read/update and audit. `Inquiry` carries independently encrypted contact_name/contact_phone/contact_email/company/message/items, key_version/revision, converted_at/pii_purged_at. All anonymous association and assignment derive from the admitted link, never client IDs.

- [x] Add regression tests: admitted submission 201, ciphertext round-trip, no anonymous read, three accepted submissions per source and fourth 429, source isolation, missing/wrong origin/cookie, body/member/quantity limits, keyed replay and mismatch, transaction rollback on inbox/audit failure.
- [x] Run the inquiry integration tests and verify the encrypted intake behavior.
- [x] Validate same-origin writes against the request origin, browser admission nonce and completion age. Apply 3/min source budget plus link/tenant budgets and 16KiB JSON size. Normalize and encrypt every contact/free-text field before persistence.
- [x] Persist submission, HMAC-bound anonymous idempotency receipt and safe inbox/outbox atomically; retries cannot duplicate notifications. Scope replay to tenant/link/generation/browser/route/key and recheck admission.
- [x] Add masked list, strict status update, PII disclosure with durable success/failure audits and non-PII CSV whitelist. Test all permission/reauth combinations, current owner transfer, tampered field envelopes and unavailable auditing.
- [x] Register explicit OpenAPI and manifest permissions and test their equality.

## Task 2: Lifecycle, visits and retention

Files: new `migrations/000014_share_privacy.*.sql`, `internal/bootstrap/share_lifecycle.go`, `share_retention.go`, `share_visit.go`, existing share customer/price/access adapters, worker scheduler, dedicated integration tests.

Interfaces: `cleanupSharePrivacy(ctx, database, now) error`; `sharePrivacyDailyCleanup(database) func(context.Context,time.Time) error`. Management visits use the same live link scope and a separate `share:visit:list` permission, recent session authentication and audit. Successful root admission writes `share_visit` in the same transaction as view_count.

- [x] Add migration and retention boundary tests: new/converted/terminated expired versus fresh rows, ciphertext removed, safe metadata preserved, visits cutoff inclusive at 90d, and daily retry on failure. Approved audit expiry uses the database clock and exact event/resource pairs, with microsecond boundary, rollback and unrelated-event protection tests.
- [x] Add lifecycle matrix tests: customer disabled/deleted/transferred and quote disabled/deleted revoke relevant active links atomically; reactivation does not revive old links; unrelated tenant/customer links unaffected; audit failure rolls everything back.
- [x] Add only required immutable timestamps, retention markers/indexes and anonymous receipt storage. Erase to invalid authenticated envelopes while preserving existing NOT NULL constraints; down restores the prior schema without restoring erased PII.
- [x] Implement bounded cleanup batches, durable counts/watermarks and audit retention of safe share events only. Integrate daily cleanup into the existing leader scheduler and log safe failures for retry.
- [x] Verify root view+visit atomicity, no raw IP/UA columns in the projection, scoped/recent/audited visit reads and 90-day deletion.

## Task 3: Public exposure isolation

Files: `internal/kernel/httpx/engine.go`, dedicated exposure middleware/tests, bootstrap HTTP assembly, `deploy/` public reverse-proxy allowlist, `scripts/check-share-exposure.ps1`, executable checklist.

- [x] Add regression tests showing anonymous access to healthz/readyz/metrics/system-info is refused before any dependencies are probed; authenticated internal access retains current output.
- [x] Inject a live JWT guard from bootstrap and default-deny missing system-route authentication. Preserve metric/readiness behavior under authenticated tests.
- [x] Provide a strict method/path reverse-proxy allowlist and deny the four system endpoints in public configuration, including method, encoded-path and query variants. Disable raw URI/referrer/access logging and document trusted proxy constraints.
- [x] Execute the actual public configuration locally and record each endpoint's 404/403 with a reusable check command.

## Task 4: Acceptance and final evidence

- [x] `go test ./internal/modules/share/... ./internal/kernel/httpx -count=1`.
- [x] `go test ./internal/bootstrap -run '^TestShare(Inquiry|Privacy|Visit|Lifecycle|Exposure).*Integration' -count=1 -p 1 -v`.
- [x] `powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-test-groups.ps1`.
- [x] `powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1 -Integration -Migration`.
- [x] `git diff --check`; `rg -n 'TODO|not implemented' internal`; file/function length check; synthetic canary checks across database/log/errors/export/inbox/outbox/idempotency.
- [x] Record individual outputs and final M6 status in the task card, report deviations and stop.
