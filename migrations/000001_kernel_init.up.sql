BEGIN;

CREATE TABLE tenant (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code text NOT NULL,
    name text NOT NULL,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended')),
    settings jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_tenant_code ON tenant (code);

INSERT INTO tenant (id, code, name) OVERRIDING SYSTEM VALUE VALUES (1, 'default', 'default');
SELECT setval(pg_get_serial_sequence('tenant', 'id'), 1);

CREATE TABLE sys_setting (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    group_key text NOT NULL,
    item_key text NOT NULL,
    value jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_by bigint,
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_sys_setting_tenant_group_item ON sys_setting (tenant_id, group_key, item_key);

CREATE TABLE audit_log (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    actor_type text NOT NULL CHECK (actor_type IN ('user', 'system', 'api_client')),
    actor_id bigint,
    action text NOT NULL,
    resource_type text NOT NULL,
    resource_id text NOT NULL,
    before jsonb NOT NULL DEFAULT '{}'::jsonb,
    after jsonb NOT NULL DEFAULT '{}'::jsonb,
    ip inet,
    user_agent text NOT NULL DEFAULT '',
    trace_id text NOT NULL,
    result text NOT NULL CHECK (result IN ('success', 'failure')),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_audit_log_tenant_created ON audit_log (tenant_id, created_at DESC);
CREATE INDEX idx_audit_log_resource ON audit_log (tenant_id, resource_type, resource_id);

CREATE FUNCTION reject_audit_log_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'audit_log is append-only' USING ERRCODE = '55000';
END;
$$;
CREATE TRIGGER audit_log_append_only
    BEFORE UPDATE OR DELETE OR TRUNCATE ON audit_log
    FOR EACH STATEMENT EXECUTE FUNCTION reject_audit_log_mutation();

CREATE TABLE outbox_event (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    aggregate_type text NOT NULL,
    aggregate_id text NOT NULL,
    event_type text NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'sent', 'failed')),
    attempts integer NOT NULL DEFAULT 0,
    available_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    sent_at timestamptz
);
CREATE INDEX idx_outbox_pending ON outbox_event (status, available_at) WHERE status IN ('pending', 'failed');

CREATE TABLE idempotency_key (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    scope text NOT NULL,
    key text NOT NULL,
    request_hash text NOT NULL,
    response_code integer,
    response_body jsonb NOT NULL DEFAULT '{}'::jsonb,
    status text NOT NULL DEFAULT 'processing' CHECK (status IN ('processing', 'completed')),
    expires_at timestamptz NOT NULL DEFAULT (now() + interval '24 hours'),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_idempotency_scope_key ON idempotency_key (tenant_id, scope, key);

CREATE TABLE notification (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    channel text NOT NULL CHECK (channel IN ('inbox', 'email', 'webhook')),
    target text NOT NULL,
    template text NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent', 'failed')),
    attempts integer NOT NULL DEFAULT 0,
    last_error text NOT NULL DEFAULT '',
    dedup_key text,
    created_at timestamptz NOT NULL DEFAULT now(),
    sent_at timestamptz
);
CREATE UNIQUE INDEX uq_notification_dedup ON notification (tenant_id, dedup_key) WHERE dedup_key IS NOT NULL;

CREATE TABLE file_object (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    bucket text NOT NULL,
    object_key text NOT NULL,
    original_name text NOT NULL,
    mime text NOT NULL,
    size_bytes bigint NOT NULL,
    sha256 text NOT NULL,
    purpose text NOT NULL CHECK (purpose IN ('product_image', 'export', 'snapshot')),
    uploaded_by bigint,
    created_at timestamptz NOT NULL DEFAULT now()
);

COMMIT;
