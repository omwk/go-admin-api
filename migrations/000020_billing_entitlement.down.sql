BEGIN;
DROP VIEW IF EXISTS billing_reconcile_month;
DROP TABLE billing_compensation;
DROP TABLE billing_reconcile_alert;
DROP TABLE billing_reconcile_diff;
DROP FUNCTION protect_billing_diff();
DROP TABLE usage_daily;
DROP TABLE billing_reconcile_run;
ALTER TABLE api_call_log DROP COLUMN entitlement_id, DROP COLUMN charge_source;
ALTER TABLE api_call DROP COLUMN cost_units, DROP COLUMN cost_cents, DROP COLUMN entitlement_id, DROP COLUMN charge_source;
ALTER TABLE wallet_txn DROP COLUMN posting_date, DROP COLUMN service_date, DROP COLUMN biz_id, DROP COLUMN biz_type;
ALTER TABLE call_charge DROP CONSTRAINT ck_charge_cash, DROP CONSTRAINT ck_charge_source,
    DROP COLUMN posting_date, DROP COLUMN service_date, DROP COLUMN reserved_units, DROP COLUMN entitlement_id, DROP COLUMN source;
DROP TABLE unit_txn;
DROP FUNCTION reject_billing_evidence_mutation();
DROP TABLE entitlement;
DROP TABLE pricing_plan;
COMMIT;
