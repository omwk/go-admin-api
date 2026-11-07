BEGIN;
DROP TABLE order_sync_pending;
DROP TABLE order_status_history;
DROP FUNCTION order_history_append_only();
DROP TABLE order_address;
DROP TABLE order_item;
DROP TABLE sales_order;
COMMIT;
