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

## 8. 实测交付记录（2026-11-13）

- 分支：`feat/s4-audit`，基于最新 `main`（`f7270c9`，包含 S3a `11e54f1`）。
- 查询：system 模块只读服务与 bootstrap 数据库适配器；租户条件来自认证身份，不接受客户端租户覆盖。
- 解锁：复用 identity 管理事务、行锁与可选 Idempotency-Key；审计与状态更新同事务。
- 权限和两条菜单均追加在已有数组末尾；无新增迁移、依赖或前端改动，`auth_login.go` 无改动。

逐项验收命令：

```powershell
go test ./internal/bootstrap -run 'TestSystemAuditIntegration|TestIdentityUnlockIntegration|TestSystemOpenAPI|TestAdminRouteOpenAPI' -v -count=1
go test ./internal/modules/system ./internal/modules/identity/...
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-test-groups.ps1
```

首条命令实测：`PASS; ok github.com/omwk/go-admin-api/internal/bootstrap 4.345s`，真实 PostgreSQL 隔离库，无跳过。

| # | 证据 |
|---|---|
| 1 | `TestSystemAuditIntegrationListFiltersAndTenant`：造 5 条，`total=5`，逐项 ID 与时间倒序匹配。 |
| 2 | 同上：action / result / actor_id 各自过滤、三者组合及 resource_type 过滤正确；额外验证跨租户不可见与 SQL 特殊字符不扩展查询。 |
| 3 | `TestSystemAuditIntegrationInclusiveRangeAndPagination`：from=to 精确命中边界 1 条，反向区间与非法时间均为 400/10001。 |
| 4 | 同上：第 2 页数据与 total 正确；page_size=100 为 200，101/0/负数/非数字与非法 page 为 400。 |
| 5 | `TestSystemAuditIntegrationRedactionAndTruncation`：before/after 中 password_hash/cipher/nonce 与嵌套 secret/token 均为 `***`，响应不含运行时生成的原值。 |
| 6 | 同上：超大多字节 JSON 的 before/after 合计 ≤4096 字节，合法 JSON，`truncated=true`；先脱敏再截断。 |
| 7 | `TestSystemAuditIntegrationLoginHistory`：直接写入现有 audit_log，成功/失败各 3 条；时间、用户、结果、IP/UA 完整且倒序，action 固定 identity.login。真实登录写入缺失来源信息的限制见下文。 |
| 8 | `TestSystemAuditIntegrationUnknownLoginAndWriterCompatibility`：真实未知账号登录后返回「未知账号」、resource_id=unknown、user_id=null，响应不含尝试输入的账号或凭据。 |
| 9 | `TestIdentityUnlockIntegrationLoginAndIdempotency`：解锁前登录失败；解锁后数据库 active/0/NULL，原密码正常登录。 |
| 10 | 同上：同键重放、无键再次调用、新键再次调用均为 200，审计只有 1 条；并发两个无键调用也只有 1 条审计。 |
| 11 | `TestIdentityUnlockIntegrationDisabledMissingAndRollback`：disabled 返回 409/10004 且保护字段不变；审计失败时状态和计数完整回滚。 |
| 12 | `TestSystemAuditIntegrationPermissionsAndMenus`：移除权限后两种查询均 403/20107，解锁无 user:update 时 403；未认证查询 401。 |
| 13 | `TestSystemAuditIntegrationPasswordMinimumTwelve`：最小长度 8 时 8 位密码创建返回 201；通过配置 HTTP 接口改为 12 后，8 位密码创建和改密均 400/10001；12 位改密 204 且可登录。 |
| 14 | `TestSystemAuditIntegrationPermissionsAndMenus`：/auth/menus 实际含两页；`TestAuditPermissionAndMenusAppendAfterS3a` 与 `TestPermissionRegistryIsProjectedToMenus` 通过。 |
| 15 | `TestSystemOpenAPIRoutesAndPermissionsMatch`：actual=33、manifest=33、OpenAPI=33，三个 S4 操作权限双向一致；identity 独立路由契约也通过，HTTP 成功/错误响应同时校验 schema。 |
| 16 | 全量 `scripts/verify.ps1 -Integration -Migration` 通过：vet/lint/全量测试均成功，集成测试 bootstrap `291.658s`，迁移版本 `27 → 26 → 27`，最终 `all checks passed`。迁移在一次性隔离数据库执行，结束后已删除。 |

全量验证关键输出（完整日志：`$env:TEMP/codex-s4-audit/verify.log`）：

```text
$ powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1 -Integration -Migration
== go vet ==
== golangci-lint ==
== go test ==
ok  github.com/omwk/go-admin-api/internal/bootstrap 299.835s
== integration ==
ok  github.com/omwk/go-admin-api/internal/kernel/db 6.976s
ok  github.com/omwk/go-admin-api/internal/bootstrap 291.658s
ok  github.com/omwk/go-admin-api/cmd/cli 10.832s
== migrate up/down/up ==
version=27 dirty=false
version=26 dirty=false
version=27 dirty=false
all checks passed
S4 disposable migration database removed
```

自检：

- 独立 `golangci-lint run` 已返回退出码 0；全量 verify 中 vet/lint/test 再次通过。本机无 make，verify 使用与 Makefile 相同的 `golangci-lint run` / `go test ./...`。
- Go AST 检查改动文件：单文件最大 307 行，单函数最大 80 行；命令：`go run "$env:TEMP/codex-s4-audit/check-size.go" -- $s4ChangedGo`（`$s4ChangedGo` 为 git diff 与未跟踪 Go 文件的去重列表）。
- `rg -n 'TODO|not implemented' internal` 无匹配；新增生产代码无调试打印。
- 脱敏测试检查响应不含随机生成的敏感原值；解锁审计精确断言只有 status / failed_attempts，不含凭据。
- CI 分片守卫：`Source 458 = shards 457 + excluded 1`，所有新增集成测试恰好归属 core 分片。
- `git diff --exit-code -- migrations internal/modules/identity/auth_login.go go.mod go.sum` 退出 0。
- `git diff --check` 退出 0；本机 `CGO_ENABLED=0`，race 检查须在 CI 执行，本次未声称 race 通过。

偏差 / 后续卡：

- 按 §3.2 保持现有登录审计写入逻辑；当前 `loginAudit` 未写 IP/UA，查询该类真实记录返回空字符串，不伪造来源信息。已单独登记 [FOLLOWUP-S4-LOGIN-METADATA](../proposals/backlog.md#followup-s4-login-metadata--登录审计来源信息补全待排期)。
- 摘要超限时以合法 JSON `{"summary":"已脱敏 JSON 的前缀"}` 返回；before/after 合计上限含 JSON 转义字节，保持有效 UTF-8。
