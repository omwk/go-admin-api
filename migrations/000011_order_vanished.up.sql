BEGIN;
ALTER TABLE sales_order ADD COLUMN vanished_at timestamptz NULL;
ALTER TABLE sales_order ADD COLUMN vanish_reason text NULL;
CREATE INDEX idx_order_vanished ON sales_order (tenant_id, shop_id, vanished_at DESC) WHERE vanished_at IS NOT NULL;
ALTER TABLE order_status_history DROP CONSTRAINT order_status_history_dimension_check;
ALTER TABLE order_status_history ADD CONSTRAINT order_status_history_dimension_check
    CHECK (dimension IN ('lifecycle','fulfillment','payment','after_sales','cancel','aggregate','visibility'));
COMMIT;
