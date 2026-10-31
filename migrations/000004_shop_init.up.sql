BEGIN;

CREATE TABLE platform (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code text NOT NULL,
    name text NOT NULL,
    auth_type text NOT NULL CHECK (auth_type IN ('oauth2', 'api_key', 'cookie', 'session')),
    capabilities jsonb NOT NULL DEFAULT '{}'::jsonb,
    config_schema jsonb NOT NULL DEFAULT '{}'::jsonb,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled'))
);
CREATE UNIQUE INDEX uq_platform_code ON platform (code);

CREATE TABLE shop (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    platform_id bigint NOT NULL,
    name text NOT NULL,
    external_shop_id text NOT NULL,
    region text NOT NULL DEFAULT '',
    timezone text NOT NULL,
    currency char(3) NOT NULL,
    owner_user_id bigint NOT NULL,
    dept_id bigint NOT NULL,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'paused', 'disabled')),
    settings jsonb NOT NULL DEFAULT '{}'::jsonb,
    last_sync_at timestamptz,
    version integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    CONSTRAINT fk_shop_platform FOREIGN KEY (platform_id)
        REFERENCES platform (id) ON DELETE RESTRICT,
    CONSTRAINT fk_shop_owner FOREIGN KEY (tenant_id, owner_user_id)
        REFERENCES sys_user (tenant_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_shop_dept FOREIGN KEY (tenant_id, dept_id)
        REFERENCES sys_dept (tenant_id, id) ON DELETE RESTRICT
);
CREATE UNIQUE INDEX uq_shop_tenant_id ON shop (tenant_id, id);
CREATE UNIQUE INDEX uq_shop_external ON shop (tenant_id, platform_id, external_shop_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_shop_owner ON shop (tenant_id, owner_user_id);

CREATE TABLE shop_credential (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    shop_id bigint NOT NULL,
    kind text NOT NULL CHECK (kind IN ('oauth2', 'api_key', 'cookie', 'session')),
    label text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'expiring', 'expired', 'needs_reauth', 'disabled')),
    current_version_id bigint,
    expires_at timestamptz,
    refresh_at timestamptz,
    last_checked_at timestamptz,
    last_refreshed_at timestamptz,
    refresh_strategy text NOT NULL DEFAULT 'none' CHECK (refresh_strategy IN ('auto', 'manual', 'none')),
    consecutive_failures integer NOT NULL DEFAULT 0,
    extra jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_shop_credential_shop FOREIGN KEY (tenant_id, shop_id)
        REFERENCES shop (tenant_id, id) ON DELETE RESTRICT
);
CREATE UNIQUE INDEX uq_shop_credential_tenant_id ON shop_credential (tenant_id, id);
CREATE INDEX idx_cred_shop ON shop_credential (tenant_id, shop_id, kind);
CREATE INDEX idx_cred_refresh_due ON shop_credential (status, refresh_at) WHERE status IN ('active', 'expiring');

CREATE TABLE credential_version (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    credential_id bigint NOT NULL,
    version_no integer NOT NULL CHECK (version_no > 0),
    cipher_text bytea,
    nonce bytea CHECK (nonce IS NULL OR octet_length(nonce) = 12),
    key_version integer NOT NULL CHECK (key_version > 0),
    aad text NOT NULL,
    fingerprint text NOT NULL,
    issued_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz,
    created_reason text NOT NULL CHECK (created_reason IN ('initial', 'refresh', 'manual', 'migration')),
    created_by bigint NOT NULL DEFAULT 0,
    raw_meta jsonb NOT NULL DEFAULT '{}'::jsonb,
    retired_at timestamptz,
    purge_after timestamptz,
    CONSTRAINT fk_credential_version_credential FOREIGN KEY (tenant_id, credential_id)
        REFERENCES shop_credential (tenant_id, id) ON DELETE RESTRICT
);
CREATE UNIQUE INDEX uq_cred_version ON credential_version (credential_id, version_no);
CREATE UNIQUE INDEX uq_cred_version_tenant_credential_id ON credential_version (tenant_id, credential_id, id);
CREATE INDEX idx_cred_version_purge ON credential_version (purge_after) WHERE purge_after IS NOT NULL;
ALTER TABLE shop_credential ADD CONSTRAINT fk_shop_credential_current_version
    FOREIGN KEY (tenant_id, id, current_version_id)
    REFERENCES credential_version (tenant_id, credential_id, id) ON DELETE RESTRICT;

CREATE TABLE credential_audit (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    credential_id bigint NOT NULL,
    action text NOT NULL CHECK (action IN ('create', 'read', 'check', 'refresh', 'rotate', 'disable', 'purge')),
    actor_type text NOT NULL CHECK (actor_type IN ('user', 'system', 'api_client')),
    actor_id bigint,
    result text NOT NULL CHECK (result IN ('success', 'failure')),
    error_code text NOT NULL DEFAULT '',
    error_msg text NOT NULL DEFAULT '',
    detail jsonb NOT NULL DEFAULT '{}'::jsonb,
    ip inet,
    trace_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_credential_audit_credential FOREIGN KEY (tenant_id, credential_id)
        REFERENCES shop_credential (tenant_id, id) ON DELETE RESTRICT
);
CREATE INDEX idx_cred_audit ON credential_audit (tenant_id, credential_id, created_at DESC);

INSERT INTO platform (code, name, auth_type, capabilities, config_schema) VALUES
    ('shopee', 'Shopee', 'oauth2', '{"resources":["product","order","inventory"],"token_refresh":true}', '{}'),
    ('taobao', '淘宝天猫', 'session', '{"resources":["product","order","inventory"],"token_refresh":false}', '{}'),
    ('shopify', 'Shopify', 'api_key', '{"resources":["product","order","inventory"],"token_refresh":false}', '{}');

COMMIT;
