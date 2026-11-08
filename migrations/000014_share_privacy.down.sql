BEGIN;
DROP TRIGGER IF EXISTS audit_log_share_retention ON audit_log;
DROP TRIGGER audit_log_append_only ON audit_log;
CREATE TRIGGER audit_log_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON audit_log
    FOR EACH STATEMENT EXECUTE FUNCTION reject_audit_log_mutation();
DROP FUNCTION IF EXISTS restrict_share_audit_retention();
DROP FUNCTION IF EXISTS share_audit_retention_due(text,text,timestamptz);
DROP TABLE share_privacy_run;
DROP TABLE share_inquiry_receipt;
DROP INDEX idx_share_visit_retention;
DROP INDEX idx_customer_retention;
DROP INDEX idx_inquiry_converted;
DROP INDEX idx_inquiry_retention;
ALTER TABLE customer DROP COLUMN terminated_at, DROP COLUMN pii_purged_at;
ALTER TABLE inquiry DROP COLUMN converted_at, DROP COLUMN pii_purged_at;
-- Erased ciphertext is intentionally unrecoverable after rollback.
COMMIT;
