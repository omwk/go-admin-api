-- ADR-023 / M9a: exact cash reservations and append-only financial evidence.
BEGIN;
CREATE TABLE wallet (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    app_id text NOT NULL,
    currency char(3) NOT NULL DEFAULT 'CNY' CHECK (currency = 'CNY'),
    balance_cents bigint NOT NULL DEFAULT 0,
    frozen_cents bigint NOT NULL DEFAULT 0,
    version integer NOT NULL DEFAULT 1 CHECK (version > 0),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','frozen')),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (app_id,currency),
    CHECK (balance_cents >= 0 AND frozen_cents >= 0 AND balance_cents >= frozen_cents)
);
CREATE INDEX idx_wallet_tenant ON wallet(tenant_id,app_id);

CREATE TABLE wallet_txn (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    wallet_id bigint NOT NULL,
    type text NOT NULL CHECK (type IN ('topup','freeze','settle','release','adjust','refund')),
    amount_cents bigint NOT NULL CHECK (amount_cents >= 0 OR type='adjust'),
    balance_delta_cents bigint NOT NULL,
    frozen_delta_cents bigint NOT NULL,
    balance_after_cents bigint NOT NULL CHECK (balance_after_cents >= 0),
    frozen_after_cents bigint NOT NULL CHECK (frozen_after_cents >= 0 AND frozen_after_cents <= balance_after_cents),
    version_after integer NOT NULL CHECK (version_after>0),
    call_id text NOT NULL DEFAULT '',
    idempotency_key text NOT NULL UNIQUE,
    request_hash text NOT NULL CHECK (request_hash ~ '^[0-9a-f]{64}$'),
    actor_id bigint NOT NULL DEFAULT 0,
    note text NOT NULL DEFAULT '' CHECK (length(note) <= 500),
    occurred_at timestamptz NOT NULL,
    CHECK ((type='freeze' AND balance_delta_cents=0 AND frozen_delta_cents=amount_cents AND amount_cents>0 AND call_id<>'')
        OR (type='settle' AND balance_delta_cents=-amount_cents AND frozen_delta_cents<0 AND call_id<>'')
        OR (type='release' AND balance_delta_cents=0 AND frozen_delta_cents<0 AND call_id<>'')
        OR (type IN ('topup','refund') AND balance_delta_cents=amount_cents AND frozen_delta_cents=0 AND amount_cents>0)
        OR (type='adjust' AND frozen_delta_cents=0 AND balance_delta_cents=amount_cents))
);
CREATE INDEX idx_wallet_txn_history ON wallet_txn(wallet_id,occurred_at DESC,id DESC);
CREATE INDEX idx_wallet_txn_call ON wallet_txn(tenant_id,call_id) WHERE call_id<>'';
REVOKE UPDATE,DELETE,TRUNCATE ON wallet_txn FROM PUBLIC;
CREATE FUNCTION reject_wallet_txn_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'wallet_txn is append-only' USING ERRCODE='55000';
END;
$$;
CREATE TRIGGER wallet_txn_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON wallet_txn
FOR EACH STATEMENT EXECUTE FUNCTION reject_wallet_txn_mutation();

CREATE TABLE call_charge (
    call_id text PRIMARY KEY,
    tenant_id bigint NOT NULL,
    wallet_id bigint NOT NULL,
    app_id text NOT NULL,
    currency char(3) NOT NULL CHECK (currency='CNY'),
    route_id bigint NOT NULL,
    route_version integer NOT NULL CHECK (route_version>0),
    price_cents bigint NOT NULL CHECK (price_cents>0),
    units bigint NOT NULL CHECK (units>0),
    pre_cents bigint NOT NULL CHECK (pre_cents>0),
    state text NOT NULL CHECK (state IN ('reserved','settled','released','expired')),
    actual_cents bigint NOT NULL DEFAULT 0 CHECK (actual_cents>=0 AND actual_cents<=pre_cents),
    created_at timestamptz NOT NULL,
    deadline_at timestamptz NOT NULL CHECK (deadline_at>created_at),
    finished_at timestamptz,
    CHECK (price_cents::numeric*units=pre_cents::numeric),
    CHECK ((state='reserved' AND finished_at IS NULL) OR (state<>'reserved' AND finished_at IS NOT NULL)),
    CHECK (state='settled' OR actual_cents=0)
);
CREATE INDEX idx_call_charge_pending ON call_charge(deadline_at,call_id) WHERE state='reserved';

CREATE TABLE recharge_code (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    code_hash text NOT NULL UNIQUE CHECK (code_hash ~ '^[0-9a-f]{64}$'),
    amount_cents bigint NOT NULL CHECK (amount_cents>0),
    currency char(3) NOT NULL DEFAULT 'CNY' CHECK (currency='CNY'),
    status text NOT NULL DEFAULT 'unused' CHECK (status IN ('unused','used','void')),
    used_by_app_id text NOT NULL DEFAULT '',
    used_at timestamptz,
    expires_at timestamptz,
    created_by bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((status='used' AND used_at IS NOT NULL AND used_by_app_id<>'') OR (status<>'used' AND used_at IS NULL AND used_by_app_id=''))
);

ALTER TABLE api_route ADD COLUMN price_cents bigint NOT NULL DEFAULT 0 CHECK (price_cents>=0),
    ADD COLUMN units_per_call bigint NOT NULL DEFAULT 1 CHECK (units_per_call>0),
    ADD COLUMN currency char(3) NOT NULL DEFAULT 'CNY' CHECK (currency='CNY');
ALTER TABLE api_call ADD COLUMN app_id text NOT NULL DEFAULT '',
    ADD COLUMN billing_mode text NOT NULL DEFAULT 'free',
    ADD COLUMN price_cents bigint NOT NULL DEFAULT 0,
    ADD COLUMN charge_units bigint NOT NULL DEFAULT 1,
    ADD COLUMN currency char(3) NOT NULL DEFAULT 'CNY',
    ADD COLUMN idempotency_key text NOT NULL DEFAULT '',
    ADD COLUMN request_hash text NOT NULL DEFAULT '';
ALTER TABLE api_call_log ALTER COLUMN cost_units TYPE bigint;
CREATE UNIQUE INDEX uq_api_call_request ON api_call(tenant_id,client_id,idempotency_key) WHERE idempotency_key<>'';
-- These independent financial facts are never included in the 90-day log cleanup.
-- Retain financial evidence for at least three years; unresolved reservations are protected.
COMMIT;
