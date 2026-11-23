# Gateway Observability Implementation Plan

> **For agentic workers:** Execute this plan inline, verifying each task before proceeding.

**Goal:** Implement BE-IDN-029 on `feat/be-idn-029-gateway-observability`, based on `98b28f7`, without frontend changes.

**Architecture:** Management routes call a gateway observation service and bootstrap persistence adapter. Manual trials sign a fresh v2 request and dispatch it through the existing open handler with a server-only context marker. The existing `charge_source` column persists the manual-test identity; no migration is needed.

**Tech Stack:** Go, Gin, GORM, PostgreSQL, Redis, existing OpenAPI YAML.

**Spec:** `docs/tasks/BE-IDN-029-gateway-observability.md`, `docs/design/api-gateway-console.md`.

## Global Constraints

- Logs and summaries never select request or response bodies.
- Trials check quotas but skip quota increments, charges and meter writes; log `manual_test`.
- Preserve tenant boundaries; cross-tenant observations require the configured platform super administrator.
- Default UTC range: 24 hours; maximum: 30 days; pagination: 1/20, maximum size 100.
- No new dependencies, no edits to merged migrations, no frontend work.

## Tasks

- [x] Add observation DTOs, range validation, list and aggregate queries. Verify filters, pagination, trace, privacy, range limits, terminal-state aggregation and tenant scope through HTTP integration tests.
- [x] Add bounded, server-signed manual trials and wire management routes. Verify successful and denied trials, IP/client/route gates, quota checks without increments, zero billing writes, response truncation and body non-retention.
- [x] Register `gateway:client:manage` and `gateway:call:read`; verify repeated seed grants the complete catalog to the built-in super administrator.
- [x] Add management OpenAPI and bidirectional route/response contract tests.
- [x] Run focused tests, SQL manual aggregate comparisons, meter before/after counts, shard guard and full `scripts/verify.ps1 -Integration`. Record all twelve acceptance items and cleanup in the task evidence.

## Verification Commands

```powershell
go test ./internal/modules/gateway ./internal/modules/identity/permission
go test ./internal/bootstrap -run '^TestGatewayObservability' -count=1 -v
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-test-groups.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1 -Integration
```

All database acceptance fixtures use isolated databases and are removed by test cleanup. Existing migration integration tests verify up/down/up.
