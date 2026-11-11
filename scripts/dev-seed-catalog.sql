-- 本地开发用演示数据：类目树 + 120 个 SPU + 240 个 SKU。
--
-- 用法（本地 Postgres 容器）：
--   Get-Content scripts/dev-seed-catalog.sql | docker exec -i deploy-postgres-1 \
--     psql -U commercehub_test -d commercehub_test -v ON_ERROR_STOP=1
--
-- 仅用于开发与前端联调，不要在生产执行。

BEGIN;

-- 1) 类目：5 个一级 + 10 个二级
INSERT INTO category (id, tenant_id, parent_id, name, path, level, sort, status)
OVERRIDING SYSTEM VALUE
VALUES
  (1, 1, 0, '电子产品', '1', 1, 1, 'active'),
  (2, 1, 0, '家居用品', '2', 1, 2, 'active'),
  (3, 1, 0, '服饰鞋包', '3', 1, 3, 'active'),
  (4, 1, 0, '美妆个护', '4', 1, 4, 'active'),
  (5, 1, 0, '食品饮料', '5', 1, 5, 'active'),
  (6, 1, 1, '手机配件', '1.6', 2, 1, 'active'),
  (7, 1, 1, '电脑外设', '1.7', 2, 2, 'active'),
  (8, 1, 2, '厨房用具', '2.8', 2, 1, 'active'),
  (9, 1, 2, '收纳整理', '2.9', 2, 2, 'active'),
  (10, 1, 3, '男装', '3.10', 2, 1, 'active'),
  (11, 1, 3, '女装', '3.11', 2, 2, 'active'),
  (12, 1, 4, '护肤', '4.12', 2, 1, 'active'),
  (13, 1, 4, '彩妆', '4.13', 2, 2, 'active'),
  (14, 1, 5, '零食', '5.14', 2, 1, 'active'),
  (15, 1, 5, '饮品', '5.15', 2, 2, 'active')
ON CONFLICT (id) DO NOTHING;

-- 2) 120 个 SPU
INSERT INTO product_spu (id, tenant_id, code, title, subtitle, brand, category_id,
                         main_image, status, source, price_min_cents, price_max_cents, sku_count)
OVERRIDING SYSTEM VALUE
SELECT
  n,
  1,
  'DEMO-' || lpad(n::text, 4, '0'),
  '演示商品 ' || n,
  '用于前端联调的示例商品',
  '品牌' || ((n - 1) % 8 + 1),
  ((n - 1) % 15) + 1,
  '',
  CASE (n % 3) WHEN 0 THEN 'active' WHEN 1 THEN 'active' ELSE 'draft' END,
  'manual',
  (1000 + n * 10)::bigint,
  (2000 + n * 10)::bigint,
  2
FROM generate_series(1, 120) AS n
ON CONFLICT (id) DO NOTHING;

-- 3) 每个 SPU 两个 SKU
INSERT INTO product_sku (id, tenant_id, spu_id, code, barcode, title, price_cents, cost_cents,
                         currency, stock_cached, attributes, status)
OVERRIDING SYSTEM VALUE
SELECT
  (n * 10 + k),
  1,
  n,
  'DEMO-' || lpad(n::text, 4, '0') || '-' || k,
  '',
  '规格 ' || CASE k WHEN 1 THEN '标准' ELSE '大号' END,
  (1000 + n * 10 + k * 500)::bigint,
  (600 + n * 5)::bigint,
  'CNY',
  (n * 3 + k)::integer,
  '{}'::jsonb,
  'active'
FROM generate_series(1, 120) AS n
CROSS JOIN generate_series(1, 2) AS k
ON CONFLICT (id) DO NOTHING;

-- 4) 用 SKU 回填冗余列
UPDATE product_spu p
SET price_min_cents = s.min_price,
    price_max_cents = s.max_price,
    sku_count = s.cnt,
    updated_at = now()
FROM (
  SELECT spu_id, MIN(price_cents) AS min_price, MAX(price_cents) AS max_price, COUNT(*) AS cnt
  FROM product_sku
  WHERE status = 'active'
  GROUP BY spu_id
) s
WHERE p.id = s.spu_id;

-- 5) 推进自增序列
-- 插入时用了显式 id（OVERRIDING SYSTEM VALUE），序列不会自动前进。
-- 不修正的话，后端第一次自增就会撞上已存在的 id（duplicate key）。
SELECT setval('product_spu_id_seq', (SELECT COALESCE(MAX(id), 1) FROM product_spu));
SELECT setval('product_sku_id_seq', (SELECT COALESCE(MAX(id), 1) FROM product_sku));
SELECT setval('category_id_seq', (SELECT COALESCE(MAX(id), 1) FROM category));

COMMIT;

-- 校验
SELECT
  (SELECT count(*) FROM category) AS categories,
  (SELECT count(*) FROM product_spu) AS spus,
  (SELECT count(*) FROM product_sku) AS skus;
