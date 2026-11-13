BEGIN;
INSERT INTO sys_dict(tenant_id,type,name,builtin)
SELECT tenant.id,seed.type,seed.name,true FROM tenant CROSS JOIN (VALUES
 ('order_status','订单状态'),('user_status','用户状态'),('sync_run_status','同步运行状态')
) AS seed(type,name) ON CONFLICT(tenant_id,type) DO NOTHING;
INSERT INTO sys_dict_item(tenant_id,dict_id,label,value,sort,builtin)
SELECT d.tenant_id,d.id,seed.label,seed.value,seed.sort,true FROM sys_dict d JOIN (VALUES
 ('order_status','待付款','pending_payment',1),('order_status','已付款','paid',2),
 ('order_status','已发货','shipped',3),('order_status','已完成','completed',4),
 ('order_status','已取消','cancelled',5),('order_status','已退款','refunded',6),
 ('user_status','正常','active',1),('user_status','停用','disabled',2),('user_status','锁定','locked',3),
 ('sync_run_status','等待中','pending',1),('sync_run_status','运行中','running',2),
 ('sync_run_status','成功','succeeded',3),('sync_run_status','失败','failed',4)
) AS seed(type,label,value,sort) ON seed.type=d.type WHERE d.builtin
ON CONFLICT(tenant_id,dict_id,value) DO NOTHING;
COMMIT;
