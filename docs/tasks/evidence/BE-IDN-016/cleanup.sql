UPDATE auth_token SET revoked_at=NULL WHERE tenant_id=1 AND user_id=109 AND purpose='verify' AND id=59 AND used_at IS NULL AND revoked_at=(SELECT created_at FROM auth_token WHERE id=60 AND tenant_id=1 AND user_id=109); -- rows=1
DELETE FROM notify_outbox WHERE (tenant_id=1 AND id=97) OR (tenant_id=27 AND id=98); -- rows=2
DELETE FROM notify_delivery WHERE (tenant_id=1 AND id=97) OR (tenant_id=27 AND id=98); -- rows=2
DELETE FROM auth_token WHERE (tenant_id=1 AND id=60 AND user_id=109) OR (tenant_id=27 AND id=61 AND user_id=115); -- rows=2
DELETE FROM sys_user_role WHERE tenant_id=27 AND user_id=115; -- rows=1
DELETE FROM password_history WHERE tenant_id=27 AND user_id=115; -- rows=1
DELETE FROM sys_user WHERE tenant_id=27 AND id=115 AND username='be_idn_016_mail_1118'; -- rows=1
DELETE FROM sys_role WHERE tenant_id=27 AND name='邮件验收管理员'; -- rows=1
DELETE FROM channel_provider WHERE tenant_id=27 AND id=7; -- rows=1
DELETE FROM sys_dept WHERE tenant_id=27 AND name='验收根部门'; -- rows=1
DELETE FROM sys_setting WHERE tenant_id=27; -- rows=0
DELETE FROM tenant WHERE id=27 AND code='be-idn-016-mail'; -- rows=1
