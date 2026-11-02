BEGIN;
CREATE TABLE sync_job (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    shop_id bigint NOT NULL,
    resource_type text NOT NULL,
    cron text NOT NULL,
    enabled boolean NOT NULL DEFAULT false,
    options jsonb NOT NULL DEFAULT '{}'::jsonb,
    last_run_id bigint,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_sync_job ON sync_job (tenant_id, shop_id, resource_type);
CREATE TABLE sync_cursor (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    shop_id bigint NOT NULL,
    resource_type text NOT NULL,
    cursor_type text NOT NULL CHECK (cursor_type IN ('timestamp', 'page_token', 'id_watermark')),
    cursor_value text NOT NULL DEFAULT '',
    cursor_meta jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_sync_cursor ON sync_cursor (tenant_id, shop_id, resource_type);
CREATE TABLE sync_run (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    shop_id bigint NOT NULL,
    job_id bigint NOT NULL,
    resource_type text NOT NULL,
    trigger text NOT NULL CHECK (trigger IN ('cron', 'manual', 'webhook', 'retry')),
    status text NOT NULL CHECK (status IN ('running', 'success', 'partial', 'failed', 'cancelled')),
    stats jsonb NOT NULL DEFAULT '{}'::jsonb,
    error_code text NOT NULL DEFAULT '',
    error_msg text NOT NULL DEFAULT '',
    started_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    trace_id text NOT NULL,
    triggered_by bigint
);
CREATE INDEX idx_sync_run ON sync_run (tenant_id, shop_id, resource_type, started_at DESC);
CREATE INDEX idx_sync_run_running ON sync_run (status) WHERE status = 'running';
CREATE TABLE sync_error (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id bigint NOT NULL,
    external_id text NOT NULL DEFAULT '',
    stage text NOT NULL CHECK (stage IN ('fetch', 'map', 'validate', 'persist')),
    error_code text NOT NULL,
    message text NOT NULL,
    raw_id bigint,
    retryable boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_sync_error_run ON sync_error (run_id);
CREATE TABLE dead_letter (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    shop_id bigint NOT NULL,
    resource_type text NOT NULL,
    external_id text NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    reason text NOT NULL,
    attempts integer NOT NULL DEFAULT 0,
    next_retry_at timestamptz,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'replayed', 'discarded')),
    created_at timestamptz NOT NULL DEFAULT now(),
    replayed_at timestamptz
);
CREATE INDEX idx_dead_letter ON dead_letter (tenant_id, status, next_retry_at);

-- ADR-016: reserve replay metadata only; no arbitrary historical executor in M3.
CREATE TABLE replay_run (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    platform_id bigint NOT NULL,
    shop_id bigint NOT NULL,
    resource_type text NOT NULL,
    mapping_version_id bigint NOT NULL,
    from_raw_id bigint NOT NULL,
    to_raw_id bigint NOT NULL,
    status text NOT NULL CHECK (status IN ('running', 'success', 'partial', 'failed')),
    stats jsonb NOT NULL DEFAULT '{}'::jsonb,
    started_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    triggered_by bigint NOT NULL
);
COMMIT;
