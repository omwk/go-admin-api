-- 本地开发用部门数据：两层结构，供前端多部门场景验证。
--
-- 用法：
--   Get-Content scripts/dev-seed-depts.sql | docker exec -i deploy-postgres-1 `
--     psql -U commercehub_test -d commercehub_test -v ON_ERROR_STOP=1
--
-- 仅用于开发与前端联调，不要在生产执行。

BEGIN;

INSERT INTO sys_dept (id, tenant_id, parent_id, name, path, sort, status)
OVERRIDING SYSTEM VALUE
VALUES
  (2, 1, 1, '电商事业部', '1.2', 1, 'active'),
  (3, 1, 1, '客服中心',   '1.3', 2, 'active'),
  (4, 1, 1, '财务部',     '1.4', 3, 'active'),
  (5, 1, 2, '商品组',     '1.2.5', 1, 'active'),
  (6, 1, 2, '订单组',     '1.2.6', 2, 'active'),
  (7, 1, 3, '售后组',     '1.3.7', 1, 'active'),
  (8, 1, 4, '对账组',     '1.4.8', 1, 'active')
ON CONFLICT (id) DO NOTHING;

-- 显式 id 插入后必须推进序列，否则后端自增会撞主键（见 dev-seed-catalog.sql）
SELECT setval('sys_dept_id_seq', (SELECT COALESCE(MAX(id), 1) FROM sys_dept));

COMMIT;

SELECT id, parent_id, name, path FROM sys_dept ORDER BY path;
