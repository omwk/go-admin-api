BEGIN;

DROP TABLE credential_refresh_reconciliation;
ALTER TABLE credential_refresh_run DROP CONSTRAINT fk_refresh_run_stage;
DROP TABLE credential_refresh_stage;
DROP TABLE credential_refresh_run;
ALTER TABLE shop_credential DROP COLUMN retry_budget;
ALTER TABLE shop_credential DROP COLUMN refresh_generation;
ALTER TABLE shop_credential DROP CONSTRAINT shop_credential_status_check;
UPDATE shop_credential SET status='expiring' WHERE status='refreshing';
ALTER TABLE shop_credential ADD CONSTRAINT shop_credential_status_check
    CHECK (status IN ('active','expiring','expired','needs_reauth','disabled'));

COMMIT;
