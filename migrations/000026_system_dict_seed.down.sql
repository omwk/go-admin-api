BEGIN;
DELETE FROM sys_dict_item item USING sys_dict d
WHERE item.tenant_id=d.tenant_id AND item.dict_id=d.id AND item.builtin AND d.builtin
AND d.type IN ('order_status','user_status','sync_run_status');
DELETE FROM sys_dict d WHERE d.builtin AND d.type IN ('order_status','user_status','sync_run_status')
AND NOT EXISTS(SELECT 1 FROM sys_dict_item item WHERE item.tenant_id=d.tenant_id AND item.dict_id=d.id);
COMMIT;
