-- ADR-022: durable logical calls, atomic success metering and UTC admission quotas.
BEGIN;
CREATE TABLE api_call (
    call_id text PRIMARY KEY CHECK (call_id ~ '^[0-9A-HJKMNP-TV-Z]{26}$'),
    tenant_id bigint NOT NULL,
    client_id bigint NOT NULL,
    route_id bigint NOT NULL,
    route_version integer NOT NULL CHECK (route_version > 0),
    trace_id text NOT NULL,
    request_id text NOT NULL DEFAULT '' CHECK (length(request_id) <= 64),
    method text NOT NULL,
    path text NOT NULL,
    ip_hash text NOT NULL CHECK (ip_hash ~ '^[0-9a-f]{64}$'),
    called_at timestamptz NOT NULL,
    deadline_at timestamptz NOT NULL CHECK (deadline_at > called_at),
    state text NOT NULL DEFAULT 'processing' CHECK (state IN ('processing','succeeded','failed')),
    admitted boolean NOT NULL DEFAULT false,
    status_code integer NOT NULL DEFAULT 0,
    error_code text NOT NULL DEFAULT '',
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    result jsonb NOT NULL DEFAULT 'null'::jsonb CHECK (octet_length(result::text) <= 2097152),
    result_hash text NOT NULL DEFAULT '',
    metered_units integer NOT NULL DEFAULT 0 CHECK (metered_units IN (0,1)),
    finished_at timestamptz,
    CHECK (metered_units = 0 OR (state = 'succeeded' AND admitted)),
    CHECK (state <> 'succeeded' OR (status_code >= 200 AND status_code < 300 AND error_code = '' AND result <> 'null'::jsonb)),
    CHECK ((state = 'processing' AND finished_at IS NULL) OR (state <> 'processing' AND finished_at IS NOT NULL))
);
CREATE INDEX idx_api_call_pending ON api_call(deadline_at,call_id) WHERE state = 'processing';

CREATE TABLE api_call_meter (
    call_id text PRIMARY KEY,
    tenant_id bigint NOT NULL,
    client_id bigint NOT NULL,
    route_id bigint NOT NULL,
    called_at timestamptz NOT NULL,
    units integer NOT NULL CHECK (units = 1)
);
CREATE INDEX idx_api_call_meter_client ON api_call_meter(tenant_id,client_id,called_at);

CREATE TABLE api_quota (
    tenant_id bigint NOT NULL,
    client_id bigint NOT NULL,
    quota_key text NOT NULL,
    admitted_count bigint NOT NULL DEFAULT 0 CHECK (admitted_count >= 0),
    reset_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id,client_id,quota_key)
);

ALTER TABLE api_call_log
    ADD COLUMN call_id text,
    ADD COLUMN request_id text NOT NULL DEFAULT '',
    ADD COLUMN route_version integer NOT NULL DEFAULT 1,
    ADD COLUMN state text NOT NULL DEFAULT 'processing' CHECK (state IN ('processing','succeeded','failed')),
    ADD COLUMN admitted boolean NOT NULL DEFAULT false,
    ADD COLUMN attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    ADD COLUMN result_hash text NOT NULL DEFAULT '',
    ADD COLUMN metered_units integer NOT NULL DEFAULT 0 CHECK (metered_units IN (0,1)),
    ADD COLUMN finished_at timestamptz,
    ADD CONSTRAINT uq_api_call_log_call UNIQUE (call_id,called_at),
    ADD CONSTRAINT ck_api_call_log_meter CHECK (metered_units = 0 OR (state = 'succeeded' AND admitted));
COMMIT;
