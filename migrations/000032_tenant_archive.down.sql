BEGIN;
DROP INDEX idx_tenant_deleted_at;
DROP INDEX idx_tenant_live_created;
ALTER TABLE tenant DROP COLUMN deleted_at;
COMMIT;
