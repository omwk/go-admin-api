-- ADR-023 / M9b: one source per call, unit evidence and reconciliation.
BEGIN;
CREATE TABLE pricing_plan (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL,
    code text NOT NULL,
    name text NOT NULL,
    currency char(3) NOT NULL CHECK (currency='CNY'),
    price_cents bigint NOT NULL CHECK (price_cents>0),
    quota_type text NOT NULL CHECK (quota_type IN ('calls','units')),
    unit_count bigint NOT NULL CHECK (unit_count>0),
    valid_days integer NOT NULL CHECK (valid_days BETWEEN 0 AND 36500),
    route_id bigint NOT NULL DEFAULT 0 CHECK (route_id>=0),
    status text NOT NULL CHECK (status IN ('active','disabled')),
    version integer NOT NULL DEFAULT 1 CHECK (version>0),
    UNIQUE (tenant_id,code)
);
CREATE TABLE entitlement (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL,
    app_id text NOT NULL,
    currency char(3) NOT NULL CHECK (currency='CNY'),
    kind text NOT NULL CHECK (kind IN ('free','paid')),
    quota_type text NOT NULL CHECK (quota_type IN ('calls','units')),
    total_units bigint NOT NULL CHECK (total_units>0),
    remaining_units bigint NOT NULL,
    reserved_units bigint NOT NULL DEFAULT 0,
    starts_at timestamptz NOT NULL,
    ends_at timestamptz,
    route_id bigint NOT NULL DEFAULT 0 CHECK (route_id>=0),
    source text NOT NULL CHECK (source IN ('purchase','gift')),
    source_ref text NOT NULL,
    plan_id bigint NOT NULL DEFAULT 0,
    plan_version integer NOT NULL DEFAULT 0,
    purchase_cents bigint NOT NULL DEFAULT 0 CHECK (purchase_cents>=0),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','expired','exhausted')),
    version integer NOT NULL DEFAULT 1 CHECK (version>0),
    created_by bigint NOT NULL,
    created_at timestamptz NOT NULL,
    grant_key text NOT NULL UNIQUE,
    request_hash text NOT NULL,
    CHECK (remaining_units>=0 AND reserved_units>=0 AND reserved_units<=remaining_units AND remaining_units<=total_units),
    CHECK (ends_at IS NULL OR ends_at>starts_at),
    CHECK ((source='purchase' AND kind='paid' AND plan_id>0 AND plan_version>0 AND purchase_cents>0) OR (source='gift' AND purchase_cents=0))
);
CREATE INDEX idx_entitlement_available ON entitlement(tenant_id,app_id,currency,kind,ends_at,id) WHERE status='active';
CREATE INDEX idx_entitlement_expiry ON entitlement(ends_at,id) WHERE status='active' AND ends_at IS NOT NULL;
CREATE TABLE unit_txn (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL,
    app_id text NOT NULL,
    currency char(3) NOT NULL CHECK (currency='CNY'),
    entitlement_id bigint NOT NULL,
    call_id text NOT NULL DEFAULT '',
    type text NOT NULL CHECK (type IN ('grant','reserve','release','consume','expire','refund')),
    amount bigint NOT NULL CHECK (amount>0),
    remaining_delta bigint NOT NULL,
    reserved_delta bigint NOT NULL,
    remaining_after bigint NOT NULL CHECK (remaining_after>=0),
    reserved_after bigint NOT NULL CHECK (reserved_after>=0 AND reserved_after<=remaining_after),
    version_after integer NOT NULL CHECK (version_after>0),
    idempotency_key text NOT NULL UNIQUE,
    occurred_at timestamptz NOT NULL,
    service_date date NOT NULL,
    posting_date date GENERATED ALWAYS AS ((occurred_at AT TIME ZONE 'UTC')::date) STORED,
    CHECK ((type IN ('grant','refund') AND remaining_delta=amount AND reserved_delta=0)
        OR (type='reserve' AND remaining_delta=0 AND reserved_delta=amount AND call_id<>'')
        OR (type='release' AND remaining_delta=0 AND reserved_delta=-amount AND call_id<>'')
        OR (type='consume' AND remaining_delta=-amount AND reserved_delta=-amount AND call_id<>'')
        OR (type='expire' AND remaining_delta=-amount AND reserved_delta=0))
);
CREATE UNIQUE INDEX uq_unit_txn_version ON unit_txn(entitlement_id,version_after);
CREATE INDEX idx_unit_txn_call ON unit_txn(tenant_id,call_id) WHERE call_id<>'';

ALTER TABLE call_charge ADD COLUMN source text NOT NULL DEFAULT 'wallet' CHECK (source IN ('pending','wallet','free','paid')),
    ADD COLUMN entitlement_id bigint NOT NULL DEFAULT 0,
    ADD COLUMN reserved_units bigint NOT NULL DEFAULT 0 CHECK (reserved_units>=0),
    ADD COLUMN service_date date GENERATED ALWAYS AS ((created_at AT TIME ZONE 'UTC')::date) STORED,
    ADD COLUMN posting_date date GENERATED ALWAYS AS ((finished_at AT TIME ZONE 'UTC')::date) STORED,
    ADD CONSTRAINT ck_charge_source CHECK ((source IN ('pending','wallet') AND entitlement_id=0 AND reserved_units=0) OR (source IN ('free','paid') AND entitlement_id>0 AND reserved_units>0)),
    ADD CONSTRAINT ck_charge_cash CHECK (source IN ('pending','wallet') OR actual_cents=0);
ALTER TABLE wallet_txn ADD COLUMN biz_type text NOT NULL DEFAULT 'api_call' CHECK (biz_type IN ('api_call','funding','subscription','compensation')),
    ADD COLUMN biz_id text NOT NULL DEFAULT '',
    ADD COLUMN service_date date,
    ADD COLUMN posting_date date GENERATED ALWAYS AS ((occurred_at AT TIME ZONE 'UTC')::date) STORED;
ALTER TABLE api_call ADD COLUMN charge_source text NOT NULL DEFAULT 'none',
    ADD COLUMN entitlement_id bigint NOT NULL DEFAULT 0,
    ADD COLUMN cost_cents bigint NOT NULL DEFAULT 0,
    ADD COLUMN cost_units bigint NOT NULL DEFAULT 0;
ALTER TABLE api_call_log ADD COLUMN charge_source text NOT NULL DEFAULT 'none',
    ADD COLUMN entitlement_id bigint NOT NULL DEFAULT 0;
-- Backfill only the new projections from M9a receipts; original cash evidence stays immutable.
UPDATE api_call c SET charge_source='wallet',cost_cents=h.actual_cents
FROM call_charge h WHERE h.tenant_id=c.tenant_id AND h.call_id=c.call_id;
UPDATE api_call_log l SET charge_source='wallet'
FROM call_charge h WHERE h.tenant_id=l.tenant_id AND h.call_id=l.call_id;

CREATE TABLE billing_reconcile_run (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL,
    service_date date NOT NULL,
    posting_date date NOT NULL,
    cutoff_at timestamptz NOT NULL,
    actor_id bigint NOT NULL,
    trigger text NOT NULL CHECK (trigger IN ('daily','manual','compensation')),
    command_key text NOT NULL DEFAULT '',
    status text NOT NULL CHECK (status IN ('running','completed')),
    last_call_id text NOT NULL DEFAULT '',
    calls bigint NOT NULL DEFAULT 0,
    differences bigint NOT NULL DEFAULT 0,
    finished_at timestamptz
);
CREATE INDEX idx_billing_run_day ON billing_reconcile_run(tenant_id,service_date,id DESC);
CREATE UNIQUE INDEX uq_billing_run_command ON billing_reconcile_run(tenant_id,actor_id,command_key) WHERE command_key<>'';
CREATE UNIQUE INDEX uq_billing_daily_run ON billing_reconcile_run(tenant_id,service_date) WHERE trigger='daily' AND status='completed';
CREATE TABLE usage_daily (
    tenant_id bigint NOT NULL,
    app_id text NOT NULL,
    currency char(3) NOT NULL CHECK (currency='CNY'),
    service_date date NOT NULL,
    posting_date date NOT NULL,
    calls bigint NOT NULL,
    metered_calls bigint NOT NULL,
    metered_cents bigint NOT NULL,
    charged_cents bigint NOT NULL,
    entitlement_units bigint NOT NULL,
    run_id bigint NOT NULL,
    cutoff_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id,app_id,currency,service_date)
);
CREATE TABLE billing_reconcile_diff (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id bigint NOT NULL,
    tenant_id bigint NOT NULL,
    app_id text NOT NULL,
    currency char(3) NOT NULL,
    service_date date NOT NULL,
    posting_date date NOT NULL,
    call_id text NOT NULL DEFAULT '',
    kind text NOT NULL,
    expected bigint NOT NULL,
    actual bigint NOT NULL,
    reference_id bigint NOT NULL DEFAULT 0,
    fingerprint text NOT NULL,
    status text NOT NULL DEFAULT 'open' CHECK (status IN ('open','resolved')),
    created_at timestamptz NOT NULL,
    resolved_at timestamptz,
    resolved_by bigint,
    UNIQUE (run_id,fingerprint),
    CHECK (expected<>actual)
);
CREATE INDEX idx_billing_diff_open ON billing_reconcile_diff(tenant_id,service_date,id) WHERE status='open';
-- Monthly coverage is derived from completed dated runs, without issuing any bill.
CREATE VIEW billing_reconcile_month AS
WITH latest AS (
    SELECT DISTINCT ON (tenant_id,service_date) tenant_id,service_date,cutoff_at
    FROM billing_reconcile_run WHERE status='completed' AND trigger<>'compensation'
    ORDER BY tenant_id,service_date,id DESC
), months AS (
    SELECT DISTINCT tenant_id,date_trunc('month',service_date)::date period_start FROM latest
), coverage AS (
    SELECT m.tenant_id,m.period_start,(m.period_start+interval '1 month')::date period_end,
        coalesce(min(d.day::date) FILTER (WHERE l.service_date IS NULL),(m.period_start+interval '1 month')::date) covered_through,
        count(l.service_date) covered_days,min(l.cutoff_at) cutoff_at
    FROM months m CROSS JOIN LATERAL generate_series(m.period_start::timestamp,(m.period_start+interval '1 month'-interval '1 day')::timestamp,interval '1 day') d(day)
    LEFT JOIN latest l ON l.tenant_id=m.tenant_id AND l.service_date=d.day::date
    GROUP BY m.tenant_id,m.period_start
)
SELECT c.*,(SELECT count(*) FROM billing_reconcile_diff d WHERE d.tenant_id=c.tenant_id
    AND d.service_date>=c.period_start AND d.service_date<c.period_end AND d.status='open') open_differences
FROM coverage c;
CREATE TABLE billing_reconcile_alert (
    diff_id bigint PRIMARY KEY,
    delivered_at timestamptz
);
CREATE TABLE billing_compensation (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL,
    diff_id bigint NOT NULL,
    actor_id bigint NOT NULL CHECK (actor_id>0),
    occurred_at timestamptz NOT NULL,
    action text NOT NULL,
    reason text NOT NULL CHECK (length(reason) BETWEEN 1 AND 500),
    before_value jsonb NOT NULL,
    after_value jsonb NOT NULL,
    idempotency_key text NOT NULL UNIQUE,
    verification_run_id bigint NOT NULL
);
CREATE FUNCTION reject_billing_evidence_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'billing evidence is immutable' USING ERRCODE='55000';
END;
$$;
CREATE TRIGGER unit_txn_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON unit_txn FOR EACH STATEMENT EXECUTE FUNCTION reject_billing_evidence_mutation();
CREATE TRIGGER billing_compensation_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON billing_compensation FOR EACH STATEMENT EXECUTE FUNCTION reject_billing_evidence_mutation();
CREATE FUNCTION protect_billing_diff() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP<>'UPDATE' THEN
        RAISE EXCEPTION 'reconciliation observation is immutable' USING ERRCODE='55000';
    END IF;
    IF (to_jsonb(NEW)-'status'-'resolved_at'-'resolved_by') IS DISTINCT FROM (to_jsonb(OLD)-'status'-'resolved_at'-'resolved_by') THEN
        RAISE EXCEPTION 'reconciliation observation is immutable' USING ERRCODE='55000';
    END IF;
    IF OLD.status='resolved' OR NEW.status<>'resolved' OR NEW.resolved_by IS NULL OR NEW.resolved_at IS NULL
        OR NOT EXISTS (SELECT 1 FROM billing_compensation WHERE diff_id=OLD.id AND tenant_id=OLD.tenant_id AND actor_id=NEW.resolved_by) THEN
        RAISE EXCEPTION 'resolution requires compensation evidence' USING ERRCODE='55000';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER billing_diff_immutable BEFORE UPDATE OR DELETE ON billing_reconcile_diff FOR EACH ROW EXECUTE FUNCTION protect_billing_diff();
CREATE TRIGGER billing_diff_no_truncate BEFORE TRUNCATE ON billing_reconcile_diff FOR EACH STATEMENT EXECUTE FUNCTION reject_billing_evidence_mutation();
REVOKE UPDATE,DELETE,TRUNCATE ON unit_txn,billing_compensation FROM PUBLIC;
REVOKE DELETE,TRUNCATE ON billing_reconcile_diff FROM PUBLIC;
COMMIT;
