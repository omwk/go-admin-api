BEGIN;
DROP INDEX idx_order_vanished;
ALTER TABLE sales_order DROP COLUMN vanish_reason;
ALTER TABLE sales_order DROP COLUMN vanished_at;
-- Retain the wider history constraint: visibility history is append-only and survives rollback.
COMMIT;
