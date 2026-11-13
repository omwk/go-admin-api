# S4 · 审计查询、登录历史与解锁

- 类型：系统层补全（`docs/design/system-layer-plan.md` S4）
- 预计：**2.5 人日**
- 分支：`feat/s4-audit`
- 依赖：S2（密码策略配置化已完成）

---

## 1. 现状

| 能力 | 现状 |
|---|---|
| 审计写入 | ✅ 已覆盖登录、授权、部门、配置、字典等 |
| **审计查询** | ❌ 表有数据，无任何接口 |
| **登录历史** | ⚠️ 已写入 `audit_log`（`action=identity.login`），但无查询接口 |
| **解锁** | ❌ 锁定靠 `status=locked` + `locked_until` + `failed_attempts`，只能改状态绕开，无专用接口 |
| **密码策略** | ✅ S2 已配置化（`kernel/setting/security.go`） |

**本卡不新增表、不新增迁移。** 登录历史直接查 `audit_log`。

## 2. 接口

### 2.1 审计日志

```
GET /admin/audit-logs?actor_id=&action=&resource_type=&result=&from=&to=&page=1&page_size=20
```

- 权限：`system:audit:list`
- 只读，无写接口
- 时间参数 RFC3339；`from`/`to` 为闭区间
- 默认按 `created_at desc`
- 返回 `before`/`after` 摘要，**不返回敏感字段**（如密码哈希、密文）

### 2.2 登录历史

```
GET /admin/login-logs?user_id=&result=&from=&to=&page=1&page_size=20
```

- 权限：`system:audit:list`
- 数据源：`audit_log WHERE action='identity.login'`
- 列：时间、用户（或「未知账号」）、结果、IP、User-Agent
- **失败记录不得泄露「账号是否存在」**：现有写入逻辑对未知账号用 `resource_id='unknown'`，
  接口返回时保持一致

### 2.3 解锁

```
POST /admin/users/{id}/unlock
```

- 权限：`system:user:update`
- 行为：`status: locked → active`，`failed_attempts = 0`，`locked_until = NULL`
- 审计：`action=system:user:unlock`，before/after 只记状态与计数，不记密码
- 幂等：已解锁账号再调用返回 200（幂等成功），不报错
- 保护：不能解锁 `disabled` 状态的账号（那是管理员主动停用）→ 409

## 3. 关键约束

### 3.1 与 S3a 的并行

两个分支会同时改：
- `internal/modules/identity/permission/registry.go`
- `internal/modules/identity/seed_catalog.go`

规则（**必须遵守**，否则合并冲突）：

- 新增权限码**追加到数组末尾**，不要插入中间
- 新增菜单**追加到 `seedMenus()` 末尾**，父路径用已有的 `/system`
- **不新增迁移**；若确实不可避免，用 `000028`（S3a 占 `000027`）

### 3.2 登录审计已有写入点

`auth_login.go` 的 `loginAudit` 已在成功/失败时写入。
本卡**不修改写入逻辑**，只加查询。若发现字段缺失（如 IP/UA 未落库），
在报告里说明并单独提卡，不要顺手改。

### 3.3 审计的可读性

`before`/`after` 是 jsonb，直接原样返回可能很大。
接口应：
- 单条最大 4KB，超出截断并标记 `truncated: true`
- 已知敏感键（`password_hash`、`cipher`、`nonce`、`secret`、`token`）一律替换为 `"***"`

## 4. 菜单投影

```
审计日志   /system/audit-logs   system:audit:list
登录历史   /system/login-logs   system:audit:list
```

两页挂在「系统」目录下，追加在部门页之后。S1b 的投影一致性测试会强制校验。

## 5. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 审计列表 | 造 5 条审计后返回 5 条，按时间倒序 |
| 2 | 过滤 | `action`、`result`、`actor_id` 单独与组合过滤正确 |
| 3 | 时间范围 | `from`/`to` 闭区间正确；反向区间返回 400 |
| 4 | 分页 | `page_size` 上限 100；越界返回 400 |
| 5 | 脱敏 | 响应中不含 `password_hash`/`cipher`/`secret`，值为 `***` |
| 6 | 截断 | 超 4KB 的 before/after 被截断且 `truncated=true` |
| 7 | 登录历史 | 成功与失败各 3 条，字段齐全（时间/结果/IP/UA） |
| 8 | 未知账号 | 失败记录显示「未知账号」，不泄露账号是否存在 |
| 9 | 解锁 | 锁定账号解锁后 `status=active`、计数归零、可正常登录 |
| 10 | 解锁幂等 | 再次调用返回 200，不报错、不重复审计 |
| 11 | 停用保护 | 对 `disabled` 账号解锁 → 409 |
| 12 | 权限 | 无 `system:audit:list` 查询 → 403；无 `system:user:update` 解锁 → 403 |
| 13 | 密码策略生效 | 把 `password_min_length` 改为 12，注册/改密时 8 位密码被拒（验证 S2 配置真的接上了） |
| 14 | 菜单投影 | `/auth/menus` 出现两个新页面；投影一致性测试通过 |
| 15 | 契约 | `system.yaml` 新增接口双向一致 |
| 16 | 全量 | `scripts/verify.ps1 -Integration -Migration` 通过 |

## 6. 不做

- 审计导出
- 审计告警（异常行为检测）
- 解锁审批流
- 修改登录审计写入逻辑

## 7. Codex prompt

```
实现 docs/tasks/S4-audit.md。

必读：本卡、internal/kernel/audit/（写入侧）、
internal/modules/identity/auth_login.go（loginAudit）、
internal/modules/identity/permission/registry.go、
internal/modules/identity/seed_catalog.go、api/openapi/system.yaml

范围：
1. GET /admin/audit-logs（过滤/分页/脱敏/截断）
2. GET /admin/login-logs（数据源 audit_log，action=identity.login）
3. POST /admin/users/{id}/unlock（幂等，disabled 返回 409）
4. 菜单与权限投影：审计日志、登录历史

并行约束（重要）：
- 与 S3a 分支同时改 registry.go 与 seed_catalog.go
- 新增权限码与菜单一律追加到末尾
- 不新增迁移；确需时用 000028

验收按卡片 §5 十六条逐条贴证据，其中第 13 条要真实验证
「配置改成 12 位后 8 位密码被拒」。
完成后停下报告，不要做前端。
```
