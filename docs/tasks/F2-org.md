# F2 · 组织完整：部门 API 与系统管理回读

- 类型：平台层补全（前端 FE-1.5 的前置）
- 预计：**3 人日**
- 分支：`feat/f2-org`
- 依赖：无

---

## 1. 背景

`docs/api/inventory.md` §10 列出 12 个缺失的读接口，加上完全没有的部门接口。
它们全是 GET，不改表、不加锁、不动事务。

没有它们，前端做不出用户/角色/菜单三个管理页：

```
授权页打开 → 不知道当前勾了哪些菜单/权限
建用户     → 不知道有哪些部门可选
```

## 2. 接口清单

### 部门

| 接口 | 用途 |
|---|---|
| `GET /admin/depts` | 部门树（含 id/parent_id/name/排序/状态/成员数） |
| `GET /admin/depts/{id}` | 部门详情 |

部门表已存在，本轮**只读**；增删改在 F4。

### 用户

| 接口 | 用途 |
|---|---|
| `GET /admin/users/{id}` | 详情（含部门、状态、创建时间） |
| `GET /admin/users/{id}/roles` | 已分配角色 id 列表 |

### 角色

| 接口 | 用途 |
|---|---|
| `GET /admin/roles/{id}` | 详情 |
| `GET /admin/roles/{id}/menus` | 已分配菜单 id |
| `GET /admin/roles/{id}/permissions` | 已分配权限 id |
| `GET /admin/roles/{id}/depts` | 已分配数据范围部门 |

### 菜单与同步任务

| 接口 | 用途 |
|---|---|
| `GET /admin/menus/{id}` | 菜单详情 |
| `GET /admin/sync-jobs` | 同步任务列表 |
| `GET /admin/sync-jobs/{id}` | 任务详情（cron、状态、游标） |

### 导出与凭据

| 接口 | 用途 |
|---|---|
| `GET /admin/exports` | 商品导出任务列表 |
| `GET /admin/orders/exports` | 订单导出任务列表 |
| `GET /admin/credentials/{cid}/versions` | 凭据历史版本（M7 已保留 90 天） |

## 3. 实现约束

- 全部为读接口，**不加新表、不加锁**
- 必须走既有的 scope 校验：越权返回 404，不是 403
- 关联回读返回 id 数组即可，不要返回完整对象（避免 N+1 与超大响应）
- 部门树一次查询组装，禁止 N+1
- 同步任务列表与导出列表分页，默认 20、上限 100
- 全部补进 `api/openapi/*.yaml`

## 4. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 部门树 | 3 层结构正确；成员数为 0 时不报错 |
| 2 | 用户详情 | 部门、状态、时间齐全；不存在返回 404 |
| 3 | 角色回读 | 给角色分配 3 个菜单后，GET 返回这 3 个 id |
| 4 | 越权 | 跨租户访问返回 404 |
| 5 | 同步任务列表 | 分页正确；无任务时返回空数组 |
| 6 | 导出列表 | 按 created_at 倒序 |
| 7 | 凭据版本 | 轮换两次后返回 2 条历史 |
| 8 | 契约 | `api/openapi` 更新，且与实现一致 |
| 9 | 全量 | `scripts/verify.ps1 -Integration -Migration` 通过 |

## 5. 不做

- 部门增删改（F4）
- 用户/角色/菜单的写接口（已存在）
- 数据范围的计算逻辑（已存在，只回读）

## 6. Codex prompt

```
实现 docs/tasks/F2-org.md。

必读：本卡、docs/api/inventory.md §10、api/openapi/identity.yaml、
internal/modules/identity/、internal/bootstrap/identity_admin_reads.go

范围：12 个读接口 + 部门树（只读），全部补进 OpenAPI。

约束：
- 不加表、不加锁、不改事务
- scope 越权返回 404
- 关联回读只返回 id 数组
- 部门树单次查询组装，禁止 N+1

验收按卡片 §4 九条逐条贴证据。
完成后停下报告，不要做 F1/F3。
```
