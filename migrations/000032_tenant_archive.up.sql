BEGIN;
ALTER TABLE tenant ADD COLUMN deleted_at timestamptz;
CREATE INDEX idx_tenant_live_created ON tenant (created_at DESC, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX idx_tenant_deleted_at ON tenant (deleted_at) WHERE deleted_at IS NOT NULL;
COMMIT;
