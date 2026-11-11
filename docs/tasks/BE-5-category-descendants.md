# BE-5 · 类目后代筛选

- 类型：后端补全（FE-2 契约缺口 P2）
- 预计：**0.5 人日**
- 分支：`feat/be-5-category-descendants`
- 依赖：无（`category.path` 已存在）

---

## 1. 背景

`GET /admin/products?category_id=1`（电子产品）只返回直接挂在 1 下的商品，
不包含子类目 6（手机配件）、7（电脑外设）的商品。

运营选大类只出 8 条，会以为数据丢了。

## 2. 方案（已选 A）

新增可选查询参数 `include_descendants`（bool，默认 false）：

```
GET /admin/products?category_id=1&include_descendants=true
  → 返回类目 1 及其所有后代的商品
```

### 实现

`category` 表已有 `path` 字段（格式 `1`、`1.6`、`1.6.9`），直接前缀匹配：

```sql
WHERE (p.category_id = ?
   OR (? AND c.path LIKE ? || '.%'))
```

- 不传或 `false`：行为与现在完全一致（精确匹配）
- 不传 `category_id` 时该参数无效，不报错

### 索引

确认 `category(path)` 有可用索引；若无，评估是否需要（预计数据量小，可先不加）。

## 3. 影响面

| 接口 | 是否支持 |
|---|---|
| `GET /admin/products` | ✅ 本轮 |
| `GET /admin/listings` | ✅ 同参数，保持一致 |
| `GET /admin/inventories` | ❌ 不在本轮 |
| 导出 `POST /admin/products/export` | ✅ 请求体里的筛选需透传该参数 |

## 4. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 默认不变 | 不传参数时 total 与引入前一致（8） |
| 2 | 含后代 | `category_id=1&include_descendants=true` → total 等于 1+6+7 之和 |
| 3 | 叶子类目 | 对叶子类目开启该参数，结果与关闭时相同 |
| 4 | 无类目参数 | 只传 `include_descendants=true` → 不报错，行为等同未传 |
| 5 | 列表一致性 | `listings` 接口同参数结果符合预期 |
| 6 | 导出 | 导出任务记录的筛选含该参数，产物行数与列表一致 |
| 7 | 契约 | `catalog.yaml` 声明参数，类型校验拒绝非布尔值 |
| 8 | 性能 | 现有数据集下 SQL 仍为单次查询，无 N+1 |
| 9 | 全量 | `scripts/verify.ps1 -Integration -Migration` 通过 |

## 5. 不做

- 多类目选择（`category_id` 仍为单个）
- 类目 CRUD
- 递归 CTE（`path` 前缀已足够）

## 6. Codex prompt

```
实现 docs/tasks/BE-5-category-descendants.md。

必读：本卡、internal/modules/catalog/product_types.go、
internal/bootstrap/catalog_product_read.go、api/openapi/catalog.yaml、
docs/tasks/BE-4-catalog-contract.md（上一轮同类改动）

范围：
1. 新增可选参数 include_descendants（bool，默认 false）
2. products 与 listings 两个列表接口都支持；导出请求体透传
3. 用 category.path 前缀匹配实现后代筛选，禁止递归 CTE
4. OpenAPI 声明参数；非布尔值返回 400

约束：
- 默认行为必须与现在完全一致
- 不得引入 N+1；SQL 计数守卫保持
- 不传 category_id 时该参数不生效，也不报错

验收按卡片 §4 九条逐条贴证据。
完成后停下报告。
```
