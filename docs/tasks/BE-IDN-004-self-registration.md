# BE-IDN-004 · 可配置的租户自助注册

- ID: BE-IDN-004
- 类型: 新增
- 仓库: 后端
- 状态: 已完成（本地验收通过）
- 预计: 2 人日
- 分支: `feat/be-idn-004-self-registration`
- 依赖: BE-IDN-001（租户 code 登录）、BE-IDN-002（首用户超管）

---

## 1. 背景

当前只有「邀请注册」，没有自助注册。用户希望**按租户**决定是否放开：

```
默认关闭（与现在一致）
某个租户想开放 → 打开开关，其用户可自己注册
```

## 2. 开关

存放在 `tenant.settings`（jsonb，已存在）：

```json
{ "allow_self_registration": false }
```

读取：`tenant.settings->>'allow_self_registration'`，缺省 `false`。

管理接口：

```
GET  /admin/settings/tenant           平台管理员或租户超管读取本租户设置
PUT  /admin/settings/tenant           修改 allow_self_registration
```

权限：`system:tenant:manage`（平台管理员）或本租户 `system:setting:update`。

> 复用系统设置的校验与审计机制，不新建表。

## 3. 注册接口

```
POST /auth/register          （匿名）
{ "tenant": "acme", "account": "alice", "password": "...", "email": "a@b.com" }
```

行为：

| 条件 | 结果 |
|---|---|
| 租户不存在 | 401/20101，与「企业代码、账号或密码错误」同一文案 |
| 开关关闭 | 403/20111「该企业未开放自助注册」 |
| 开关开启 + 账号可用 | 创建用户（`status=disabled`，待邮箱验证），返回 201 |
| 账号/邮箱已存在 | 202，与首次创建的 data 结构一致，不返回占用细节；待验证邮箱重发验证邮件 |

### 3.1 首个用户

若该租户**没有任何 active 超管**，注册者通过邮箱验证后自动成为超管
（复用 BE-IDN-002 的首用户规则，事务内判断）。

否则按租户默认角色授予：

```
tenant.settings.default_role_code   -- 缺省为空 → 只给最小权限（无角色）
```

> 空角色意味着登录后只能看到首页空态——这是安全的默认值。

### 3.2 邮箱验证

```
注册 → 生成 verify 令牌（purpose="verify"，24 小时，单次）
    → 发送验证邮件（走 F3 通知通道；未配置通道时返回可复制链接，仅 dev/test）
POST /auth/register/verify { "token": "..." }
    → 用户 status: disabled → active
    → 若为首个用户，同一事务内 is_super=true
```

**未验证用户不能登录**：返回 403/20111「请先完成邮箱验证」。

### 3.3 防滥用

| 措施 | 规则 |
|---|---|
| 每租户注册速率 | 每小时上限（`tenant.settings.registration_hourly_limit`，默认 10） |
| 同邮箱重复注册 | 重发验证邮件，不创建新用户；返回同样的 202 形状 |
| 账号枚举 | 已存在账号也返回 202（不暴露是否已注册） |

> 与忘记密码一致：**响应形状不随账号存在性变化**。

## 4. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 默认关闭 | 未配置的租户注册 → 403/20111 |
| 2 | 开启 | 开启后注册 → 201，用户状态 disabled |
| 3 | 未验证不能登录 | 验证前登录 → 403「请先完成邮箱验证」 |
| 4 | 验证后登录 | 调用 verify → active，可登录 |
| 5 | 首用户超管 | 空租户首个注册者验证后为超管 |
| 6 | 非首用户 | 已有超管的租户，注册者按默认角色（缺省无角色） |
| 7 | 租户隔离 | 同名账号可在不同租户分别注册 |
| 8 | 账号枚举 | 已存在账号重复注册 → 202，形状与首次相同 |
| 9 | 限流 | 超过每小时上限 → 429/10005 |
| 10 | 未知租户 | 401，文案与密码错误一致 |
| 11 | 邮箱重复 | 同租户同邮箱 → 202；待验证邮箱重发邮件，不创建新用户 |
| 12 | 审计 | 注册、验证、开关变更均写审计 |
| 13 | 通道缺失 | 未配置邮件通道时 verify 链接在 dev 返回、prod 只 202 |
| 14 | 契约 | OpenAPI 更新 |
| 15 | 全量 | `verify` 通过 |

## 5. 不做

- 手机号注册
- 图形验证码（速率限制替代）
- 注册审批流
- 租户自助开通（租户仍由平台管理员创建）

## 6. Codex prompt

```
实现 docs/tasks/BE-IDN-004-self-registration.md。

分支：feat/be-idn-004-self-registration（从最新 main 开）

必读：本卡、internal/modules/identity/account_invite.go（令牌与首用户事务）、
account_accept.go、tenant_recovery.go（首用户规则）、
internal/kernel/setting/（设置校验与审计）、
internal/kernel/notifier/channel（发信）、api/openapi/identity.yaml

改动：
1. tenant.settings 增加 allow_self_registration / default_role_code / registration_hourly_limit
2. GET/PUT /admin/settings/tenant（复用设置机制与审计）
3. POST /auth/register（开关校验、账号枚举防护、限流、201）
4. 邮箱验证：POST /auth/register/verify（purpose=verify，24h 单次）
5. 首用户验证后自动超管（复用 BE-IDN-002 事务内判断）
6. 未验证登录返回 403「请先完成邮箱验证」

约束：
- 默认关闭
- 首次创建返回 201，重复账号或邮箱返回 202，data 结构一致（实现前已确认 A）；prod 未配置邮件通道时统一 202
- 未配置邮件通道时沿用 F3 的降级（dev 返回链接，prod 只 202）
- 复用现有令牌表，不新增表

验收按卡片 §4 十五条逐条贴证据。
完成后停下报告，不要做前端。
```

## 7. 实施计划与契约澄清

已确认采用 A：首次 201，重复账号/邮箱 202，响应均为 `accepted` 与仅 dev/test 可见的 `dev_verify_url`；不返回用户 ID 或占用原因。HTTP 状态仍区分首次与重复，完整隐藏存在性不在此决定内。prod 缺失邮件通道时首次也返回 202。

本计划直接保存在任务卡中，避免增加范围外的计划文档；按顺序在当前会话执行。共享设置列表增加 tenant 分组，因此同时同步 `api/openapi/system.yaml` 的分组枚举及既有契约/默认值测试，保留路由、权限、分组集合的严格全等断言。

- [x] 设置：在 `internal/kernel/setting/` 注册 tenant 三项默认值；`internal/bootstrap/system_setting.go` 将该组读写映射至 `tenant.settings`，保留其他 JSON 键；复用设置验证、事务与摘要审计。GET/PUT 均允许超管或 `system:tenant:manage` / `system:setting:update`，始终操作当前租户。
- [x] 注册：在 identity 新增注册服务、HTTP 处理与 repo 端口，在 bootstrap 实现持久化及 Redis 租户小时预算；通过 `RegisterAccountRegistrationRoutes` 装配。注册使用根部门，先保存 disabled 用户与密码历史，再事务内写 24h 单次 verify 摘要、通知 outbox 和审计；重复注册不覆盖账号或密码。
- [x] 验证：按令牌摘要确定租户，再使用现有租户邀请锁、账号锁和令牌行锁；同一事务检查 active 超管、激活用户、授予默认角色、消费令牌、写审计。未验证登录仅在密码正确时返回 403/20111；其他 disabled 用户保持现有行为。
- [x] 通知：新增数据迁移注册 `identity.verify` 模板，复用 F3 加密 outbox、渠道缺失告警与异步投递；仅待验证 disabled 用户允许接收验证邮件，链接凭据仍放 fragment。
- [x] 验收：新增 `TestIdentityRegistrationIntegration...` 覆盖 §4 十五条及并发、回滚、过期/重放、跨 purpose；更新 OpenAPI 并按响应校验。运行设置单测、目标集成测试、分片守卫及 `scripts/verify.ps1 -Integration -Migration`，记录输出；检查行数、空实现与敏感日志。

接口表（完整前缀 `/api/v1`）：

| 方法 | 路径 | 成功 | 权限/入参 |
|---|---|---|---|
| GET | `/admin/settings/tenant` | 200，既有 SettingGroup | 当前租户，超管或上述任一权限 |
| PUT | `/admin/settings/tenant` | 200，既有 SettingGroup | 注册设置键值对象；复用 Idempotency-Key |
| POST | `/auth/register` | 201/202，accepted 结构 | tenant/account/password/email，匿名 |
| POST | `/auth/register/verify` | 204 | token，匿名；摘要唯一确定租户 |

## 8. 验收证据（2026-11-14）

以下测试后缀均以 `TestIdentityRegistrationIntegration` 为前缀，位于 `internal/bootstrap/identity_registration*_test.go`。十三个新增集成测试全部通过，HTTP 响应同时按 `identity.yaml` 校验。

| # | 验收结果 | 测试证据（均 PASS） |
|---|---|---|
| 1 | 未配置注册开关时 403/20111 | `DefaultAndLifecycle`；设置单测另断言默认 false / 空角色 / 10 |
| 2 | 开启后首次 201，持久化 disabled、密码摘要和密码历史，无超管或角色预授予 | `DefaultAndLifecycle` |
| 3 | 正确密码但未验证时 403/20111「请先完成邮箱验证」；错误密码仍为通用 401 | `DefaultAndLifecycle` |
| 4 | verify 204 后 active，登录 200；重复 verify 400；verify purpose、24h 和单次均断言 | `DefaultAndLifecycle`、`ExpiryAndPurposeSeparation` |
| 5 | 空租户验证后自动超管；两名待验证用户并发激活，恰好一名 active 超管 | `DefaultRoleAndIsolation`、`ConcurrentFirstUsers` |
| 6 | 已有 active 超管时按本租户默认角色授予；空角色零授权 | `DefaultRoleAndIsolation`、`DefaultAndLifecycle` |
| 7 | 两个租户可分别注册并登录同名、同邮箱账号，用户与角色隔离 | `DefaultRoleAndIsolation` |
| 8 | 重复账号 202，与首次 data 字段一致；不返回占用信息、不覆盖密码 | `DuplicateAccountAndEmail`，按已确认 A |
| 9 | 小时预算满后 429/10005；重复请求计数，其他租户独立、到期恢复；八路竞争仅两路获准 | `TenantLimitAndUnknownTenant`、`ConcurrentQuotaAndRedisFailure` |
| 10 | 未知租户 401/20101，文案与登录密码错误完全一致 | `TenantLimitAndUnknownTenant` |
| 11 | 同租户重复邮箱 202，不新增用户；待验证邮箱重发，新链接生效、旧链接失效 | `DuplicateAccountAndEmail`，按已确认 A |
| 12 | 注册、验证、开关更新均有原子审计；设置记录摘要，快照不含密码、邮箱、原始令牌或链接 | `AuditAndSensitiveData` |
| 13 | dev 无通道返回可用 fragment 链接；prod 无通道只 accepted/202 并记录 F3 降级；配置后异步发送给待验证用户 | `MailFallbackAndDelivery` |
| 14 | 新接口和共享设置契约已同步；实际路由、权限、OpenAPI 双向严格匹配 | `TestAdminRouteOpenAPIAndPermissionsAreExactlyEqual`、`TestIdentityOpenAPIContainsExactlyTenDocumentedAuthRoutes`、`TestSystemOpenAPIRoutesAndPermissionsMatch`；注册测试逐响应校验 |
| 15 | vet、lint、全仓测试、独立集成、迁移 up/down/up 均通过 | `powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1 -Integration -Migration`，末行 `all checks passed` |

运行命令（缓存均在系统临时目录，不在仓库）：

```powershell
$env:GOCACHE = Join-Path $env:TEMP 'go-admin-api-build-cache'
$env:GOLANGCI_LINT_CACHE = Join-Path $env:TEMP 'go-admin-api-lint-cache'
go test -count=1 -v ./internal/kernel/setting ./internal/bootstrap -run 'TestTenantRegistration|TestIdentityRegistrationIntegration|TestIdentityOpenAPIContains'
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-test-groups.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1 -Integration -Migration
```

关键输出：

```text
targeted: 13 registration integration tests PASS
ok  github.com/omwk/go-admin-api/internal/kernel/setting 0.071s
ok  github.com/omwk/go-admin-api/internal/bootstrap 7.337s
Source 525 = shards 524 + excluded 1
PASS: every integration test has exactly one shard or an explicit exclusion.
== go vet ==
== golangci-lint ==
== go test ==
ok  github.com/omwk/go-admin-api/internal/bootstrap 327.612s
== integration ==
ok  github.com/omwk/go-admin-api/internal/kernel/db 7.129s
ok  github.com/omwk/go-admin-api/internal/bootstrap 329.941s
ok  github.com/omwk/go-admin-api/cmd/cli 22.393s
== migrate up/down/up ==
version=30 dirty=false
version=29 dirty=false
version=30 dirty=false
all checks passed
```

## 9. 自检与偏差说明

- [x] 本机无 `make`；执行同等命令 `golangci-lint run`（exit 0、无诊断）和 `go test ./...`（全部通过），并通过上述完整 verify。
- [x] 迁移命令 `go run ./cmd/cli migrate up` / `down` / `up` 已由 verify 实际执行；DB 集成测试同时覆盖全链回滚到底再 up、并发启动和结构不变量。
- [x] 新接口已列入 §7 接口表及 OpenAPI；新迁移仅插入/删除验证邮件模板，复用现有 `auth_token`，无新增表、无新增依赖。
- [x] `rg -n 'TODO|not implemented' internal` 无匹配；`git diff --check` 无空白错误。
- [x] 日志检查：人工复核新增日志调用及目标测试输出；缺通道仅记录 request_id / tenant_id / delivery_id，限流依赖错误使用固定文案。`AuditAndSensitiveData` 与 `MailFallbackAndDelivery` 检查审计、outbox 不泄漏凭据；链接保留 F3 加密存储。
- [x] `go run "$env:TEMP/be-idn-004-sizecheck.go" -- <所有改动 Go 文件>` 使用 Go AST 检查：最长文件 248 行，最长函数 62 行，均低于 400/80。
- [x] 十三个新增集成测试均由 core 分片唯一执行，守卫全量通过；未修改 CI 分片规则。
- [x] 额外通过回滚重试、跨 purpose、Redis 不可用拒绝创建、验证前角色停用/租户停用、关闭开关后已有链接验证等行为测试。

偏差：按人确认的 A 方案统一冲突条款，首次 201、重复账号/邮箱 202、data 结构一致；prod 缺邮件通道统一 202。其他偏差无。共享 system OpenAPI 和既有契约测试随 tenant 分组同步，未放宽断言。未做前端。

本地未运行 race（无 gcc）；CI race 尚待后续提交后执行。日志保存在系统临时目录 `be-idn-004-targeted.log`、`be-idn-004-contract-regressions.log`、`be-idn-004-groups.log`、`be-idn-004-verify.log`。当前未提交或推送，也未创建 PR。
