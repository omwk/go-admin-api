# BE-1 · 用户列表的 keyword 与 status 筛选

- 类型：后端功能补全（前端 FE-1 依赖）
- 预计：**0.5 人日**
- 分支：`feat/be-1-user-filters`
- 背景：前端 FE-1 已按 `keyword` / `status` 实现筛选，但后端当前忽略这两个参数
  （实测 `keyword=sales01` 与 `status=disabled` 均返回全量）。

---

## 1. 现状

`GET /api/v1/admin/users` 只声明并实现：

```
page / page_size / sort
```

代码位置：`internal/bootstrap/identity_admin_store.go`（列表查询）、
`internal/modules/identity/repo/admin.go`。

## 2. 任务

| # | 改动 |
|---|---|
| 1 | `api/openapi/identity.yaml` 的 `listUsers` 增加两个可选参数：`keyword`（string，≤256）、`status`（enum: active/disabled/locked） |
| 2 | 列表查询支持：`keyword` 对 `username` / `nickname` / `email` 做 `ILIKE '%…%'`；`status` 精确匹配 |
| 3 | 保持 `page_size` 上限 100、默认排序 `-created_at` |
| 4 | 参数非法（如未知 status）返回 `400/10001` |
| 5 | 补集成测试：命中/不命中/组合筛选/非法枚举 |

## 3. 约束

- 不得改变现有分页与排序语义
- 不得引入新依赖
- `keyword` 必须参数化查询，禁止拼接 SQL
- 空字符串视为「不筛选」
- 数据范围（scope）仍生效：筛选不能绕过行级可见性

## 4. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | keyword 命中 | 建 `sales01`/`sales02` 后查 `keyword=sales01` → total=1 |
| 2 | keyword 模糊 | `keyword=sales` → total=2 |
| 3 | status 筛选 | `status=disabled` → 只返回 disabled |
| 4 | 组合 | `keyword=sales&status=active` → 交集 |
| 5 | 非法 | `status=frozen` → 400/10001 |
| 6 | 上限 | `page_size=200` → 实际 100 |
| 7 | 无回归 | 不传参数时行为与现在一致 |
| 8 | 全量 | `scripts/verify.ps1 -Integration -Migration` 通过 |

## 5. 可粘贴的 Codex prompt

```
实现 docs/tasks/BE-1-user-filters.md。

必读：本卡、api/openapi/identity.yaml 的 listUsers、
internal/bootstrap/identity_admin_store.go、internal/modules/identity/repo/admin.go、
docs/api/local-dev.md（本地端口用 18080，Redis 16379）

改动：
1. OpenAPI 增加 keyword（≤256）与 status（active/disabled/locked）两个可选查询参数
2. 列表查询支持 keyword 对 username/nickname/email 的 ILIKE 匹配、status 精确匹配
3. 未知 status 返回 400/10001
4. 参数化查询，禁止拼接

验收按卡片 §4 八条逐条贴证据（含 curl 或集成测试输出）。
完成后停下报告，不要扩大范围。
```
