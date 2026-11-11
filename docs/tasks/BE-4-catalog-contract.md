# BE-4 · 商品接口两处契约缺口

- 类型：后端补全（FE-2 联调暴露）
- 预计：**1.5 人日**
- 分支：`feat/be-4-catalog-contract`
- 依赖：无

---

## 1. 背景

FE-2 联调时报告了三个问题。核查后**只有两个是后端的**，第三个是开发脚本的锅（已修）。

### 已排除：创建商品 500

FE-2 报告「创建无 SKU 商品返回 HTTP 500 / 10002」。

**实际根因**：`scripts/dev-seed-catalog.sql` 用 `OVERRIDING SYSTEM VALUE` 插入了固定 id，
但没有推进自增序列。后端第一次自增撞上种子数据：

```
duplicate key value violates unique constraint "product_spu_pkey"
Key (id)=(7) already exists
```

已在 `f780f50` 修正（脚本末尾补 `setval`）。修正后创建返回 201。

**但它暴露了一个真问题**，见 §2 问题 1。

---

## 2. 问题 1（P0）：未映射错误返回空响应体

### 现象

数据库唯一键冲突时：

```
HTTP 500
body: （空）
日志: status=500, error_code=0
```

前端拿不到任何信息，排查时只能靠猜 —— FE-2 因此把它误报成「创建接口坏了」。

### 期望

任何失败都必须返回标准信封：

```json
{
  "code": 10002,
  "message": "服务内部错误",
  "trace_id": "01M758...",
  "request_id": "...",
  "data": null
}
```

### 范围

- 检查错误映射的 **default 分支**：未知错误 → `500 / 10002`，不是空 body + `error_code=0`
- 检查 `httpx` 的失败写入路径：确保任何 error 都会写一次响应
- 不得把数据库原文透出给客户端（内部细节只进日志，带 trace_id）
- 日志必须包含真实错误（当前只有 `error_code:0`，看不出原因）

### 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 未知错误 | 构造一个未映射错误 → 500 + `code=10002` + 非空 body |
| 2 | 唯一键冲突 | 创建重复 code 的商品 → 409 或 500，但**必须有 code 与 trace_id** |
| 3 | 日志 | 服务端日志含原始错误与同一 trace_id |
| 4 | 不泄漏 | 响应体不含 SQL、表名、约束名 |
| 5 | 已有错误不受影响 | 400/403/404/409 的既有映射不变 |

---

## 3. 问题 2（P1）：商品列表缺类目字段

### 现象

`GET /admin/products` 的每一项没有 `category_id`，也没有类目名。前端类目列只能显示「—」。

### 期望

列表项增加：

```
category_id    int64 | null
category_name  string | null   -- 联表取，避免前端 N+1
```

### 约束

- 用一次 JOIN 或批量查字典，**禁止逐行查询**
- 类目被删或不存在时返回 `null`，不报错

### 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 字段存在 | 列表每项含 `category_id` 与 `category_name` |
| 2 | 正确性 | id=7 的商品返回类目「电脑外设」 |
| 3 | N+1 | 查询 20 条时类目查询次数为 1（SQL 计数守卫） |
| 4 | 空值 | 无类目商品返回 `null`，前端显示「—」 |
| 5 | 契约 | `catalog.yaml` 更新 |

---

## 4. 问题 3（P2，需决策）：父类目筛选不含子类目

### 现象

```
category_id=1（电子产品）→ total=8
子类目 6、7 各 8 条 → 父类目查询未包含它们
```

### 两种方案

**A. 后端支持 `include_descendants=true`**

- 优点：语义明确，前端一个开关；一次查询
- 缺点：需要递归 CTE 或 `path LIKE '1.%'`（类目表已有 `path` 字段，成本低）

**B. 保持精确匹配，前端提示「仅精确筛选当前类目」**

- 优点：零改动
- 缺点：运营体验差，选大类只出 8 条会以为数据丢了

### 建议

**选 A**，用现成的 `path` 字段：

```sql
WHERE (category_id = ? OR (? AND category_path LIKE ? || '.%'))
```

`path` 已在 `category` 表存在（格式 `1.6`），无需递归 CTE。

### 验收（若选 A）

| # | 项 | 断言 |
|---|---|---|
| 1 | 默认不变 | 不传参数时仍是精确匹配 |
| 2 | 含后代 | `include_descendants=true` + `category_id=1` → total 包含子类目 |
| 3 | 性能 | 10 万商品下 P95 不退化 20% 以上 |

---

## 5. 不做

- 类目 CRUD（F4）
- 商品列表的类目多选筛选
- 修改 `include_descendants` 之外的分页语义

---

## 6. Codex prompt

```
实现 docs/tasks/BE-4.md。

必读：本卡、internal/kernel/httpx/（错误映射与响应写入）、
internal/modules/catalog/product_http.go、internal/bootstrap/catalog_*.go、
api/openapi/catalog.yaml

范围：
1. 问题 1（P0）：未映射错误必须返回 500 + code 10002 + 非空 body + trace_id，
   日志含原始错误；不得泄漏 SQL/表名/约束名
2. 问题 2（P1）：商品列表项增加 category_id 与 category_name，单次 JOIN，禁止 N+1
3. 问题 3（P2）：先不要实现，把方案 A/B 的取舍写进报告等我确认

复现命令（本地）：
  登录 admin 后 POST /api/v1/admin/products，body:
  {"title":"x","category_id":1,"status":"draft"}
  当前返回 HTTP 500 空 body

验收按卡片 §2/§3 的表格逐条贴证据。
完成后停下报告，不要动类目筛选。
```
