# BE-IDN-011 · 租户归档与彻底删除

- ID: BE-IDN-011
- 类型: 新增
- 仓库: 后端
- 状态: 待实现
- 预计: 1.5 人日
- 分支: `feat/be-idn-011-tenant-delete`
- 依赖: S5（租户管理）、BE-IDN-002（管理员恢复）

---

## 1. 为什么现在要做

S5 的结论是「不做删除，停用即归档」。这条保守决定带来两个问题：

1. 测试/废弃租户只能停用，列表里越积越多
2. 真正要清理时只能手写 SQL 按外键顺序删 15 张表——**比提供接口更危险**

删租户是高危操作，但**不给工具不等于没人会做**，只会让做法更不可控。

## 2. 三段式模型

| 阶段 | 接口 | 状态 | 数据 | 可恢复 |
|---|---|---|---|---|
| 正常 | — | active | 有 | — |
| 停用 | `POST /admin/tenants/{id}/disable` | suspended | 有 | 立即 |
| **归档** | `POST /admin/tenants/{id}/archive` | suspended + `deleted_at` | 有 | 只有平台管理员可恢复 |
| **彻底删除** | `DELETE /admin/tenants/{id}` | 行消失 | 无 | 不可 |

### 2.1 归档

```
POST /admin/tenants/{id}/archive        归档（= 软删除）
POST /admin/tenants/{id}/restore        恢复
```

- 写 `deleted_at = now()`
- 登录、刷新、API 访问全部按停用处理（403/20111）
- 列表默认不返回归档租户；`?include_archived=true` 才显示
- 详情按 id 仍可查（便于恢复）
- 已有 active 用户的租户归档时直接成功（用户随之无法登录），不需要先停用

### 2.2 彻底删除

```
DELETE /admin/tenants/{id}?confirm=<tenant_code>
```

前置条件（全部满足才执行）：

| 条件 | 失败返回 |
|---|---|
| 平台管理员 | 404 |
| 非平台租户 | 409「平台租户不可删除」 |
| 已归档（`deleted_at` 非空） | 409「请先归档」 |
| 归档满 `tenant.purge_grace_days`（默认 7 天） | 409「归档未满 N 天」 |
| `confirm` 与租户 code 完全一致 | 400「确认码不匹配」 |

执行：

- **单事务**按外键依赖顺序删除该租户全部数据（见 §3）
- 删除前统计各表行数，写审计：`system.tenant.purge`，`after` 记录 `{code, rows:{table:count}}`
- 任一步失败整体回滚

### 2.3 配置

```
tenant.purge_grace_days   number  0-365  默认 7
```

0 表示允许归档后立即删除（仅建议测试环境）。

## 3. 级联删除范围

按当前 schema 实测，需要按顺序删除（外键在前）：

```
sys_role_permission → sys_role_menu → sys_user_role
password_history → sys_refresh_token → auth_token
notify_delivery → notify_outbox → notify_template → channel_provider
notification → notification_preference
sys_dict_item → sys_dict
sys_menu → sys_role → sys_user → sys_dept → sys_permission
tenant
```

要求：

- 清单集中在一个函数里，**新增带 tenant_id 的表时必须同步登记**（配扫描测试：
  所有 `tenant_id` 列的表必须在清单中，或显式豁免）
- 删除语句参数化，按 tenant_id 过滤
- 每表删除行数计入审计

## 4. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 归档 | `archive` 后列表默认不返回；`include_archived=true` 返回且带 deleted_at |
| 2 | 归档登录 | 该租户用户登录 403/20111 |
| 3 | 恢复 | `restore` 后可正常登录，列表恢复显示 |
| 4 | 删除前置 | 未归档删除 → 409；归档未满宽限期 → 409 |
| 5 | 确认码 | `confirm` 错误 → 400；正确 → 204 |
| 6 | 平台租户 | 归档/删除均 409 |
| 7 | 删除彻底 | 删除后 tenant 行不存在；其用户/角色/菜单/字典/令牌计数均为 0 |
| 8 | 事务 | 注入中途失败 → 所有表计数不变、租户仍存在 |
| 9 | 审计 | 一条 `system.tenant.purge`，含各表行数，不含敏感数据 |
| 10 | 清单守卫 | 新增带 tenant_id 的表未登记 → 测试失败 |
| 11 | 宽限期配置 | `purge_grace_days=0` 时归档后可立即删除 |
| 12 | 越权 | 非平台管理员调用 → 404 |
| 13 | 契约 | OpenAPI 更新，双向一致 |
| 14 | 全量 | `verify` 通过 |

## 5. 不做

- 租户数据导出（删除前备份）
- 异步后台清理（本卡为同步单事务）
- 批量删除多个租户
- 前端页面（另卡）

## 6. Codex prompt

```
实现 docs/tasks/BE-IDN-011-tenant-delete.md。

分支：feat/be-idn-011-tenant-delete（从最新 main 开）

必读：本卡、internal/modules/identity/tenant*.go、
internal/bootstrap/identity_tenant_store.go、identity_tenant_materialize.go、
migrations/ 现有 tenant 与 tenant-scoped 表、internal/kernel/setting/（配置读取）

改动：
1. 迁移 000032：tenant.deleted_at + 索引；配置 tenant.purge_grace_days（默认 7）
2. POST /admin/tenants/{id}/archive 与 /restore
3. DELETE /admin/tenants/{id}?confirm=<code>：仅平台管理员、需已归档且过宽限期
4. 级联删除清单集中实现，单事务；删除行数写审计
5. 列表支持 include_archived（默认 false）
6. 扫描测试：所有含 tenant_id 的表必须在删除清单中或显式豁免

约束：
- 平台租户不可归档/删除
- 删除不可逆；失败必须整体回滚
- 不删除其他租户的任何行

验收按卡片 §4 十四条逐条贴证据：
- 第 7 条贴删除后各表计数
- 第 8 条贴失败回滚后的计数
- 第 9 条贴审计内容
完成后停下报告，不要做前端。
```
