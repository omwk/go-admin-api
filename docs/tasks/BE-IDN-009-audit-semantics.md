# BE-IDN-009 · 审计日志语义修正

- ID: BE-IDN-009
- 类型: 修复
- 仓库: 后端
- 状态: 待实现
- 预计: 1.5 人日
- 分支: `feat/be-idn-009-audit-semantics`
- 依赖: 无

---

## 1. 现象与根因

用户看到的审计记录：

```
action: system.tenant.list
resource_id: 2 / 3 / 4 / 7 / …          ← 一次列表请求产生 12 条
before == after == {actor_tenant, target_tenant}
ip: ""  user_agent: ""
```

四处问题：

### ① 读操作逐行写审计

`tenant.go:75` 对列表里**每个非本租户的行**写一条审计。
一次 `GET /admin/tenants` → 12 条记录。当前库里 644/1781 条是这种噪音。

### ② before 与 after 被人为填成一样

`tenant.go:53` 的 `record()`：

```go
before["actor_tenant"], before["target_tenant"] = ...
after["actor_tenant"],  after["target_tenant"]  = ...   // 同一份
```

读操作本来没有"变更"，代码却给两边塞了同样的上下文。**这不是变更记录，是访问记录**，
却放在变更字段里。

### ③ IP / User-Agent 全链路缺失

`AdminService.record` 构造 `audit.Event` 时没带 IP/UA；
只有登录路径（FOLLOWUP-S4）补过。所以除登录外，审计都查不到来源。

### ④ action 命名两套风格

```
identity.login          system.tenant.list      ← 点号（1186 条）
system:dept:create      system:role:grant      ← 权限码风格（610 条）
```

同一张表两种命名，无法按前缀统一筛选。

---

## 2. 目标语义

| 事件类型 | 记录粒度 | before / after |
|---|---|---|
| **写操作**（新建/更新/删除/授权） | 每次成功变更一条 | 只记**实际变化的字段**；新建时 before=null；删除时 after=null |
| **敏感读**（跨租户读取） | **每请求一条** | before=null；after 记访问范围（哪些租户、多少条） |
| **普通读**（本租户内列表/详情） | **不写审计** | — |

并且：所有审计事件都必须带 IP 与 User-Agent。

## 3. 改动

### 3.1 读审计改为每请求一条

`TenantService.Tenants`：

```go
// 旧：for 每行写一条
// 新：收集越权访问的租户集合，循环外写一条
record(ctx, actor, "system.tenant.list", "tenant", 0, map[string]any{
    "accessed_tenants": []int64{2,3,4,...},
    "count": len(rows)-1,
})
```

`TenantService.Tenant`：仍每请求一条，`resource_id` 为目标租户。

**规则**：列表类接口无论命中多少行，最多写 1 条。

### 3.2 before / after 只记真实变化

- 更新：只放**发生变化的字段**（新旧值），未变字段不出现
- 新建：`before` 为 null，`after` 为新建后的快照（脱敏后）
- 删除：`before` 为删除前快照，`after` 为 null
- 读：`before` 为 null，`after` 只放访问范围元数据
- **before 与 after 完全相同的记录不应产生**：更新无实际变化时直接不写审计（或标记 `noop`）

### 3.3 全链路 IP / UA

在认证中间件把 `RequestMeta`（IP/UA）注入 context；
`AdminService.record` 与 `audit` 写入时从 context 取，不再依赖调用方逐个传参。

验收要求：任意写操作产生的审计都带非空 IP（本地为 127.0.0.1）。

### 3.4 action 命名统一

统一为 `<domain>.<resource>.<verb>`：

```
system.tenant.list / system.tenant.update / system.tenant.disable
system.dept.create / system.role.grant / system.user.delete
identity.login / identity.register / identity.password.reset
```

执行方式：

- 代码中所有 action 字面量改为点号风格
- 迁移把历史 `system:dept:create` 这类批量改为 `system.dept.create`
- 在 `docs/design/audit-events.md` 登记全部 action 及含义（新增即登记，配一致性测试）

### 3.5 开发数据清理

新增 `cmd/cli audit-prune --before <RFC3339>`：

- 删除指定时间之前的审计（`app.env=prod` 需显式 `--force`）
- 输出删除条数

用于清掉当前 1781 条噪音，也作为将来的保留策略工具。

## 4. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 列表只记一条 | `GET /admin/tenants`（跨 12 个租户）→ 审计新增 **1** 条 |
| 2 | 读审计内容 | 该条 before=null，after 含 `accessed_tenants` 与 `count` |
| 3 | 本租户列表不写 | 单租户用户拉自己的列表 → 审计不新增 |
| 4 | 更新只记变化 | 只改名称 → before/after 仅含 name |
| 5 | 无变化不写 | 提交与当前相同的值 → 不产生审计（或标记 noop，二选一并文档化） |
| 6 | 新建/删除 | 新建 before=null、after 非空；删除相反 |
| 7 | IP/UA | 任意写操作审计的 ip 非空、user_agent 非空 |
| 8 | 命名统一 | 全部 action 匹配 `^[a-z]+\.[a-z.]+$`，无冒号形式 |
| 9 | 历史迁移 | 旧 action 批量转换，转换前后计数一致 |
| 10 | 事件登记 | `audit-events.md` 覆盖所有 action；新增未登记 → 测试失败 |
| 11 | prune | 清理后剩余条数与 `--before` 一致；prod 无 `--force` 拒绝 |
| 12 | 全量 | `verify` 通过 |

## 5. 不做

- 审计分级（合规/运维分离）留待需要时
- 审计导出
- 请求级 access log（另属可观测性，不进 audit_log）

## 6. Codex prompt

```
实现 docs/tasks/BE-IDN-009-audit-semantics.md。

分支：feat/be-idn-009-audit-semantics（从最新 main 开）

必读：本卡、internal/modules/identity/tenant.go（record 与 Tenants）、
internal/modules/identity/admin.go（record）、internal/kernel/audit/、
internal/bootstrap/identity_tenant*.go、tenant 相关测试

改动：
1. 读审计改为每请求一条（列表汇总 accessed_tenants，不逐行）
2. before/after 只记真实变化；读操作为 null + 访问元数据；无变化不写
3. 认证中间件注入 RequestMeta，record 自动取 IP/UA
4. action 统一点号风格 + 迁移批量转换 + audit-events.md 登记与一致性测试
5. cmd/cli audit-prune --before（prod 需 --force）

约束：
- 不改变租户越权返回 404 的语义
- 审计写入失败仍要回滚业务（保持现有事务语义）
- 不新增表

验收按卡片 §4 十二条逐条贴证据；
第 1 条贴「一次请求前后 total 差值为 1」；
第 8 条贴审计表中 action 命名统计（冒号形式应为 0）。
完成后停下报告，不要做前端。
```
