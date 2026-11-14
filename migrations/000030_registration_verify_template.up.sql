BEGIN;
INSERT INTO notify_template(code,locale,subject,body,format)
VALUES ('identity.verify','zh-CN','验证注册邮箱','请在 24 小时内打开邮箱验证链接：{{.Link}}','text');
COMMIT;
