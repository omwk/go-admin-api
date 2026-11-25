# BE-IDN-033 · 授权查询与撤销

- ID: BE-IDN-033
- 类型: 新增
- 仓库: 后端
- 状态: 待实现
- 预计: 0.5 人日
- 分支: `feat/be-idn-033-grant-read`
- 依赖: BE-IDN-029/031/032

---

## 1. 问题（实测确认）

网关的**路由授权只能写，不能读、不能删**：

```
POST   /admin/api-clients/{id}/routes        创建/更新授权
DELETE /admin/api-clients/{id}/routes/{gid}  不存在
GET    /admin/api-clients/{id}/routes        不存在
```

`ManagementStore` 只有 `GrantRoute`，没有 `ListGrants` / `RevokeRoute`。

后果：

- 前端「授权回显」无法实现（FE-IDN-020 验收第 7 条）
- 授权后无法撤销，只能改状态为 `suspended`（但仍占配额、仍出现在列表里）
- 管理员无法知道某客户端被授予了哪些路由

替代方案也不成立：

| 现有 GET | 返回 | 是否含授权 |
|---|---|---|
| `/admin/api-clients` | 客户端列表 | ❌ |
| `/admin/api-routes` | 全部路由 | ❌ |
| `/admin/api-clients/{id}/secrets` | 密钥 | ❌ |

**结论：当前没有读取接口，必须新增。**

## 2. 改动

### 2.1 授权列表

```
GET /admin/api-clients/{id}/routes
→ data: [
    { "id": 12, "route_id": 3, "route_code": "usage.read",
      "route_name": "用量查询", "method": "GET", "path": "/open/v1/usage",
      "status": "active", "quota_daily": 100, "quota_monthly": 1000,
      "granted_at": "2026-11-25T10:00:00Z", "expires_at": null,
      "version": 2 }
  ]
```

- 权限：`gateway:client:list`
- 只返回本租户、本客户端的授权
- 按 `route_code` 升序
- 路由被删除时 `route_code` 回退为 `#<route_id>`

### 2.2 撤销授权

```
DELETE /admin/api-clients/{id}/routes/{grant_id}
→ 204
```

- 权限：`gateway:client:grant`
- 审计 `gateway.route.revoke`（已存在的 action）
- 不存在/跨租户 → 404
- 已撤销再撤销 → 404（幂等友好）

### 2.3 单个客户端（顺带）

```
GET /admin/api-clients/{id}
→ data: { id, app_id, name, owner_user_id, status, version, ip_whitelist }
```

- 权限：`gateway:client:list`
- 跨租户 → 404

用途：授权抽屉打开时只需拉单个客户端，不必翻列表。

## 3. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 列表 | 返回该客户端全部授权，含 route_code/name/method/path 与配额 |
| 2 | 空列表 | 未授权客户端返回 `[]` |
| 3 | 排序 | 按 route_code 升序稳定 |
| 4 | 撤销 | DELETE 204；再查列表不含该项 |
| 5 | 审计 | 撤销写一条 `gateway.route.revoke`，含 client_id/route_id |
| 6 | 幂等 | 重复撤销 → 404 |
| 7 | 跨租户 | 其他租户客户端 → 404 |
| 8 | 单客户端 | GET /api-clients/{id} 返回正确，跨租户 404 |
| 9 | 路由已删 | 授权仍返回，route_code 回退 `#<id>` |
| 10 | 契约 | OpenAPI 更新，双向一致 |
| 11 | 全量 | `verify` 通过 |

## 4. 不做

- 授权历史（谁在何时改过）
- 批量撤销

## 5. Codex prompt

```
实现 docs/tasks/BE-IDN-033-grant-read.md。

分支：feat/be-idn-033-grant-read（从最新 main 55781a0 开）

必读：本卡、internal/modules/gateway/admin_http.go、
admin_types.go（ManagementStore）、internal/modules/gateway/registry.go（Route）、
migrations/（api_client_route、api_route）、audit-events.md

改动：
1. ManagementStore 增加 ListGrants 与 RevokeGrant（含租户谓词）
2. GET /admin/api-clients/{id}/routes（join api_route 取 code/name/method/path）
3. DELETE /admin/api-clients/{id}/routes/{grant_id}（审计 gateway.route.revoke）
4. GET /admin/api-clients/{id}
5. OpenAPI 同步

约束：
- 不改现有 GrantRoute 语义
- 不改配额扣减逻辑
- 路由删除后授权记录仍可读（code 回退 #id）

验收按卡片 §3 十一条逐条贴证据：
- 第 4、5 条贴撤销后列表与审计
- 第 8 条贴跨租户 404
完成后停下报告，不要做前端。
```
