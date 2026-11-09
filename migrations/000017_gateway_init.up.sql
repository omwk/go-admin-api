-- ADR-022: independent application identities and encrypted HMAC key versions.
BEGIN;
CREATE TABLE api_client (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenant(id),
    app_id text NOT NULL UNIQUE CHECK (app_id ~ '^[0-9A-HJKMNP-TV-Z]{26}$'),
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    owner_user_id bigint NOT NULL REFERENCES sys_user(id),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','suspended')),
    ip_whitelist inet[] NOT NULL DEFAULT '{}',
    version integer NOT NULL DEFAULT 1 CHECK (version > 0),
    last_used_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id,id)
);
CREATE INDEX idx_api_client_owner ON api_client(tenant_id,owner_user_id,id);

CREATE TABLE api_client_secret (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL,
    client_id bigint NOT NULL,
    fingerprint text NOT NULL CHECK (fingerprint ~ '^[0-9a-f]{12}$'),
    cipher_text bytea NOT NULL CHECK (octet_length(cipher_text) >= 16),
    nonce bytea NOT NULL CHECK (octet_length(nonce) = 12),
    key_version integer NOT NULL CHECK (key_version > 0),
    version integer NOT NULL DEFAULT 1 CHECK (version > 0),
    secret_prefix text NOT NULL CHECK (length(secret_prefix) = 6),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','expired','revoked')),
    expires_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    FOREIGN KEY (tenant_id,client_id) REFERENCES api_client(tenant_id,id) ON DELETE RESTRICT
);
CREATE INDEX idx_client_secret ON api_client_secret(tenant_id,client_id,status);

CREATE TABLE api_route (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenant(id),
    code text NOT NULL CHECK (length(code) BETWEEN 1 AND 100),
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    capability text NOT NULL CHECK (length(capability) BETWEEN 1 AND 100),
    method text NOT NULL CHECK (method IN ('GET','HEAD')),
    path text NOT NULL CHECK (path LIKE '/open/v1/%'),
    route_group text NOT NULL CHECK (length(route_group) BETWEEN 1 AND 100),
    visibility text NOT NULL DEFAULT 'private' CHECK (visibility IN ('private','public')),
    status text NOT NULL DEFAULT 'disabled' CHECK (status IN ('enabled','disabled')),
    billing_mode text NOT NULL DEFAULT 'free' CHECK (billing_mode IN ('free','per_call','package')),
    version integer NOT NULL DEFAULT 1 CHECK (version > 0),
    created_by bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id,id),
    UNIQUE (tenant_id,code),
    UNIQUE (tenant_id,method,path)
);

CREATE TABLE api_client_route (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL,
    client_id bigint NOT NULL,
    route_id bigint NOT NULL,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','suspended')),
    quota_daily integer NOT NULL DEFAULT 0 CHECK (quota_daily >= 0),
    quota_monthly integer NOT NULL DEFAULT 0 CHECK (quota_monthly >= 0),
    version integer NOT NULL DEFAULT 1 CHECK (version > 0),
    granted_by bigint NOT NULL,
    granted_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz,
    UNIQUE (client_id,route_id),
    FOREIGN KEY (tenant_id,client_id) REFERENCES api_client(tenant_id,id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id,route_id) REFERENCES api_route(tenant_id,id) ON DELETE RESTRICT
);

-- Explicit per-order application ceilings and independent public identifiers.
CREATE TABLE api_client_resource_grant (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL,
    client_id bigint NOT NULL,
    resource_type text NOT NULL CHECK (resource_type = 'order'),
    resource_id bigint NOT NULL CHECK (resource_id > 0),
    public_id text NOT NULL UNIQUE CHECK (public_id ~ '^[0-9A-HJKMNP-TV-Z]{26}$'),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','suspended')),
    version integer NOT NULL DEFAULT 1 CHECK (version > 0),
    granted_by bigint NOT NULL,
    granted_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz,
    UNIQUE (client_id,resource_type,resource_id),
    FOREIGN KEY (tenant_id,client_id) REFERENCES api_client(tenant_id,id) ON DELETE RESTRICT
);

-- M8a creates the observation table only. M8b owns durable call identity and metering.
CREATE TABLE api_call_log (
    id bigserial NOT NULL,
    tenant_id bigint NOT NULL,
    client_id bigint NOT NULL,
    route_id bigint NOT NULL,
    trace_id text NOT NULL,
    method text NOT NULL,
    path text NOT NULL,
    status_code integer NOT NULL,
    latency_ms integer NOT NULL CHECK (latency_ms >= 0),
    req_bytes integer NOT NULL CHECK (req_bytes >= 0),
    resp_bytes integer NOT NULL CHECK (resp_bytes >= 0),
    ip_hash text NOT NULL DEFAULT '',
    error_code text NOT NULL DEFAULT '',
    billing_mode text NOT NULL DEFAULT 'free',
    cost_units integer NOT NULL DEFAULT 0,
    cost_cents bigint NOT NULL DEFAULT 0,
    billed boolean NOT NULL DEFAULT false,
    called_at timestamptz NOT NULL,
    PRIMARY KEY (id,called_at)
) PARTITION BY RANGE(called_at);
CREATE INDEX idx_call_log_client ON api_call_log(tenant_id,client_id,called_at DESC);
CREATE INDEX idx_call_log_route ON api_call_log(tenant_id,route_id,called_at DESC);
DO $$
DECLARE
    first_month timestamptz := date_trunc('month',now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC';
    boundary timestamptz;
BEGIN
    FOR month_offset IN -1..2 LOOP
        boundary := first_month + make_interval(months => month_offset);
        EXECUTE format('CREATE TABLE api_call_log_%s PARTITION OF api_call_log FOR VALUES FROM (%L) TO (%L)',
            to_char(boundary AT TIME ZONE 'UTC','YYYYMM'), boundary, boundary + interval '1 month');
    END LOOP;
END $$;
COMMIT;
