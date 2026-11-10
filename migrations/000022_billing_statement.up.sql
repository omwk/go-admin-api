-- ADR-023: immutable issued consumption bills and evidenced adjustments.
BEGIN;
CREATE TABLE billing_statement (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 tenant_id bigint NOT NULL, app_id text NOT NULL, currency char(3) NOT NULL CHECK(currency='CNY'),
 period_start date NOT NULL, period_end date NOT NULL,
 status text NOT NULL DEFAULT 'draft' CHECK(status IN ('draft','issued')),
 total_cents bigint NOT NULL DEFAULT 0, total_units bigint NOT NULL DEFAULT 0 CHECK(total_units>=0),
 cutoff_at timestamptz NOT NULL, source_hash text NOT NULL DEFAULT '', version integer NOT NULL DEFAULT 1 CHECK(version>0),
 created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, issued_at timestamptz, issued_by bigint,
 UNIQUE(tenant_id,app_id,currency,period_start),
 CHECK(period_start=date_trunc('month',period_start)::date AND period_end=(period_start+interval '1 month')::date),
 CHECK((status='draft' AND issued_at IS NULL AND issued_by IS NULL) OR (status='issued' AND issued_at IS NOT NULL AND issued_by>0))
);
CREATE TABLE billing_statement_line (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 tenant_id bigint NOT NULL, statement_id bigint NOT NULL, source_key text NOT NULL,
 kind text NOT NULL CHECK(kind IN ('call_cash','call_units','late_call_cash','late_call_units','subscription','late_subscription','refund','supplement')),
 amount_cents bigint NOT NULL, units bigint NOT NULL CHECK(units>=0), price_cents bigint NOT NULL CHECK(price_cents>=0), quantity bigint NOT NULL CHECK(quantity>0),
 route_version integer NOT NULL DEFAULT 0, entitlement_id bigint NOT NULL DEFAULT 0, call_id text NOT NULL DEFAULT '',
 original_txn_id bigint NOT NULL DEFAULT 0, original_statement_id bigint NOT NULL DEFAULT 0,
 service_date date NOT NULL, occurred_at timestamptz NOT NULL,
 UNIQUE(tenant_id,source_key)
);
CREATE INDEX idx_statement_line_page ON billing_statement_line(tenant_id,statement_id,id);
CREATE INDEX idx_statement_line_original ON billing_statement_line(tenant_id,original_txn_id) WHERE original_txn_id>0;
CREATE TABLE billing_adjustment (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 tenant_id bigint NOT NULL, app_id text NOT NULL, currency char(3) NOT NULL CHECK(currency='CNY'),
 original_txn_id bigint NOT NULL, kind text NOT NULL CHECK(kind IN ('refund','supplement')), amount_cents bigint NOT NULL CHECK(amount_cents>0),
 diff_id bigint NOT NULL DEFAULT 0, reason text NOT NULL CHECK(length(reason) BETWEEN 1 AND 500),
 evidence text NOT NULL CHECK(length(evidence) BETWEEN 1 AND 500), actor_id bigint NOT NULL,
 idempotency_key text NOT NULL UNIQUE, request_hash text NOT NULL,
 status text NOT NULL CHECK(status IN ('open','applied')), period_start date NOT NULL,
 created_at timestamptz NOT NULL, applied_at timestamptz, ledger_txn_id bigint NOT NULL DEFAULT 0,
 CHECK((status='open' AND applied_at IS NULL AND ledger_txn_id=0) OR (status='applied' AND applied_at IS NOT NULL AND ledger_txn_id>0)),
 CHECK(kind='refund' OR diff_id>0)
);
CREATE INDEX idx_adjustment_original ON billing_adjustment(tenant_id,original_txn_id,id);
CREATE INDEX idx_adjustment_open ON billing_adjustment(tenant_id,app_id,id) WHERE status='open';
CREATE UNIQUE INDEX uq_adjustment_diff ON billing_adjustment(tenant_id,diff_id) WHERE diff_id>0;

CREATE FUNCTION protect_billing_statement() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' OR OLD.status='issued' THEN RAISE EXCEPTION 'issued statement is immutable' USING ERRCODE='55000'; END IF;
 IF NEW.tenant_id<>OLD.tenant_id OR NEW.app_id<>OLD.app_id OR NEW.currency<>OLD.currency OR NEW.period_start<>OLD.period_start OR NEW.period_end<>OLD.period_end THEN
  RAISE EXCEPTION 'statement identity is immutable' USING ERRCODE='55000';
 END IF;
 IF NEW.status='issued' AND (NEW.total_cents::numeric<>(SELECT coalesce(sum(amount_cents::numeric),0) FROM billing_statement_line WHERE statement_id=OLD.id AND tenant_id=OLD.tenant_id)
  OR NEW.total_units::numeric<>(SELECT coalesce(sum(units::numeric),0) FROM billing_statement_line WHERE statement_id=OLD.id AND tenant_id=OLD.tenant_id)) THEN
  RAISE EXCEPTION 'statement totals do not match evidence' USING ERRCODE='55000';
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER billing_statement_protect BEFORE UPDATE OR DELETE ON billing_statement FOR EACH ROW EXECUTE FUNCTION protect_billing_statement();
CREATE FUNCTION protect_billing_statement_line() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE parent_id bigint; parent_tenant bigint; parent_state text;
BEGIN
 IF TG_OP='UPDATE' THEN RAISE EXCEPTION 'statement line replacement requires rebuild' USING ERRCODE='55000'; END IF;
 IF TG_OP='DELETE' THEN parent_id:=OLD.statement_id;parent_tenant:=OLD.tenant_id; ELSE parent_id:=NEW.statement_id;parent_tenant:=NEW.tenant_id; END IF;
 SELECT status INTO parent_state FROM billing_statement WHERE id=parent_id AND tenant_id=parent_tenant FOR SHARE;
 IF parent_state IS DISTINCT FROM 'draft' THEN RAISE EXCEPTION 'issued statement lines are immutable' USING ERRCODE='55000'; END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER billing_statement_line_protect BEFORE INSERT OR UPDATE OR DELETE ON billing_statement_line FOR EACH ROW EXECUTE FUNCTION protect_billing_statement_line();
CREATE FUNCTION protect_billing_adjustment() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' OR OLD.status='applied' THEN RAISE EXCEPTION 'adjustment evidence is immutable' USING ERRCODE='55000'; END IF;
 IF (to_jsonb(NEW)-'status'-'period_start'-'applied_at'-'ledger_txn_id') IS DISTINCT FROM (to_jsonb(OLD)-'status'-'period_start'-'applied_at'-'ledger_txn_id')
  OR NEW.status<>'applied' OR NOT EXISTS(SELECT 1 FROM wallet_txn WHERE id=NEW.ledger_txn_id AND tenant_id=NEW.tenant_id AND biz_type='compensation' AND biz_id=OLD.id::text) THEN
  RAISE EXCEPTION 'adjustment requires ledger evidence' USING ERRCODE='55000';
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER billing_adjustment_protect BEFORE UPDATE OR DELETE ON billing_adjustment FOR EACH ROW EXECUTE FUNCTION protect_billing_adjustment();
CREATE TRIGGER billing_statement_no_truncate BEFORE TRUNCATE ON billing_statement FOR EACH STATEMENT EXECUTE FUNCTION reject_billing_evidence_mutation();
CREATE TRIGGER billing_statement_line_no_truncate BEFORE TRUNCATE ON billing_statement_line FOR EACH STATEMENT EXECUTE FUNCTION reject_billing_evidence_mutation();
CREATE TRIGGER billing_adjustment_no_truncate BEFORE TRUNCATE ON billing_adjustment FOR EACH STATEMENT EXECUTE FUNCTION reject_billing_evidence_mutation();
REVOKE DELETE,TRUNCATE ON billing_statement,billing_adjustment FROM PUBLIC;

-- Inspection only: expiration is not authorization to delete immutable evidence.
CREATE FUNCTION billing_retention_candidates(as_of timestamptz)
RETURNS TABLE(record_type text,record_id bigint,tenant_id bigint,app_id text,retained_until timestamptz)
LANGUAGE sql STABLE AS $$
 WITH records AS (
  SELECT 'statement'::text kind,id,tenant_id,app_id,greatest(created_at,issued_at) occurred_at,status='draft' unresolved FROM billing_statement
  UNION ALL SELECT 'adjustment',id,tenant_id,app_id,greatest(created_at,applied_at),status='open' FROM billing_adjustment
  UNION ALL SELECT 'wallet_txn',x.id,x.tenant_id,w.app_id,x.occurred_at,false FROM wallet_txn x JOIN wallet w ON w.id=x.wallet_id AND w.tenant_id=x.tenant_id
  UNION ALL SELECT 'unit_txn',id,tenant_id,app_id,occurred_at,false FROM unit_txn
  UNION ALL SELECT 'compensation',c.id,c.tenant_id,d.app_id,c.occurred_at,false FROM billing_compensation c JOIN billing_reconcile_diff d ON d.id=c.diff_id AND d.tenant_id=c.tenant_id
 ) SELECT r.kind,r.id,r.tenant_id,r.app_id,r.occurred_at+interval '3 years' FROM records r
 WHERE r.occurred_at+interval '3 years'<=as_of AND NOT r.unresolved
 AND NOT EXISTS(SELECT 1 FROM billing_adjustment a WHERE a.tenant_id=r.tenant_id AND a.app_id=r.app_id AND (a.status='open' OR greatest(a.created_at,a.applied_at)+interval '3 years'>as_of))
 AND NOT EXISTS(SELECT 1 FROM billing_reconcile_diff d WHERE d.tenant_id=r.tenant_id AND d.app_id=r.app_id AND d.status='open')
 AND NOT EXISTS(SELECT 1 FROM call_charge c WHERE c.tenant_id=r.tenant_id AND c.app_id=r.app_id AND c.state='reserved');
$$;
COMMIT;
