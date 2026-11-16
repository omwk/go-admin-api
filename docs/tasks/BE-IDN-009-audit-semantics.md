# BE-IDN-009 · 审计日志语义修正

- ID: BE-IDN-009
- 类型: 修复
- 仓库: 后端
- 状态: 已实现，待验收（历史资源名称覆盖率存在偏差，见验收证据）
- 预计: 2.5 人日
- 分支: `feat/be-idn-009-audit-semantics`
- 依赖: 无

验收证据：[BE-IDN-009-audit-semantics-evidence.md](BE-IDN-009-audit-semantics-evidence.md)。

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

- 更新：只放**发生变化的字段**（新旧值），未变字段不出现；**无任何变化则不写**
- 新建：`before` 为 null，`after` 为新建后的快照（脱敏后）
- 删除：`before` 为删除前快照，`after` 为 null
- 读：`before` 为 null，`after` 只放访问范围元数据
- **before 与 after 完全相同的记录不应产生**：更新无实际变化时**不写审计**（已确认，不做 noop 标记）

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


### 3.6 显示快照（审计是给人看的）

现状：ctor_id=1、esource_id="2"，界面上只能显示「用户 #1」。
用户列表本身也不展示 id，所以**无法反查是谁、操作了什么对象**。

审计是**证据**，必须在写入时固化当时的名称——用户改名或被删除后，
历史记录仍应可读：

| 新增列 | 内容 | 示例 |
|---|---|---|
| ctor_name | 操作者快照 | dmin（或「张三（admin）」） |
| ctor_tenant_name | 操作者租户快照 | default |
| esource_label | 资源快照 | 租户 cme、用户 sales01、部门 电商事业部、字典 order_status |
| error_code | 失败时的错误码 | 10004（成功为 null） |
| error_message | 失败原因（脱敏） | 「资源冲突或仍被引用」 |

写入规则：

- 这些字段在**写入时从当前数据快照**，不做查询时 join（join 会因改名/删除而失真）
- 资源名过长截断 128 字符
- 删除类操作必须能取到名称（先查再删，把名称带进审计）
- 迁移回填：对历史行按 ctor_id / esource_id join 现有表尽力回填，
  回填不到的保持 null（前端显示 #id）

### 3.7 查询能力

GET /admin/audit-logs 增加：

| 参数 | 说明 |
|---|---|
| keyword | 模糊匹配 actor_name / resource_label / action |
| ctor_name | 精确（或前缀）匹配操作者名称 |
| ction_prefix | 如 system.tenant.，便于按模块筛选 |

仍保留现有的 ctor_id / ction / esult / 时间范围。

### 3.8 失败记录的原因

写入失败审计时带上 error_code 与 error_message（脱敏后的用户可读文案），
这样「失败」不再是一个无法追查的结果。

## 4. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 列表只记一条 | `GET /admin/tenants`（跨 12 个租户）→ 审计新增 **1** 条 |
| 2 | 读审计内容 | 该条 before=null，after 含 `accessed_tenants` 与 `count` |
| 3 | 本租户列表不写 | 单租户用户拉自己的列表 → 审计不新增 |
| 4 | 更新只记变化 | 只改名称 → before/after 仅含 name |
| 5 | 无变化不写 | 提交与当前相同的值 → 审计不新增（已确认不做 noop 记录） |
| 6 | 新建/删除 | 新建 before=null、after 非空；删除相反 |
| 7 | IP/UA | 任意写操作审计的 ip 非空、user_agent 非空 |
| 8 | 命名统一 | 全部 action 匹配 `^[a-z]+\.[a-z.]+$`，无冒号形式 |
| 9 | 历史迁移 | 旧 action 批量转换，转换前后计数一致 |
| 10 | 事件登记 | `audit-events.md` 覆盖所有 action；新增未登记 → 测试失败 |
| 11 | prune | 清理后剩余条数与 `--before` 一致；prod 无 `--force` 拒绝 |
| 12 | 显示快照 | 审计返回 actor_name / actor_tenant_name / resource_label 且与写时一致 |
| 13 | 改名不变 | 把用户改名后再查历史审计，仍显示旧名称 |
| 14 | 删除可读 | 删除被操作对象后，历史审计仍能显示其名称 |
| 15 | 失败原因 | 失败记录的 error_code 与 error_message 非空且脱敏 |
| 16 | 关键字搜索 | keyword=admin 能命中操作者；ction_prefix=system.tenant. 只返回该模块 |
| 17 | 回填 | 历史行回填后，能取到名称的行占比 ≥ 90%（贴统计） |
| 18 | 全量 | erify 通过 |

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
5. 写入时快照操作者与资源名称；失败记录带原因
6. 查询支持 keyword / actor_name / action_prefix
7. cmd/cli audit-prune --before（prod 需 --force）

约束：
- 不改变租户越权返回 404 的语义
- 审计写入失败仍要回滚业务（保持现有事务语义）
- 不新增表

验收按卡片 §4 十二条逐条贴证据；
第 1 条贴「一次请求前后 total 差值为 1」；
第 8 条贴审计表中 action 命名统计（冒号形式应为 0）。
完成后停下报告，不要做前端。
```
