# BE-IDN-003 · 个人资料与会话的字段补全

- ID: BE-IDN-003
- 类型: 修复
- 仓库: 后端
- 状态: 已实现（§4 十三条验收通过）
- 预计: 1.5 人日
- 分支: `feat/be-idn-003-profile-sessions`
- 依赖: 无（FE-UI-001 已在前端预留只读）

---

## 1. 来源

FE-UI-001 联调时两条验收只能标「部分通过」：

| 前端验收 | 缺口 |
|---|---|
| §6-5 资料展示 | `/auth/me` 不返回邮箱、手机、部门名称 |
| §6-9 会话列表 | `/auth/sessions` 缺 IP、User-Agent、当前设备标记 |

另：`PUT /auth/profile` 不支持修改手机号，而 `sys_user.phone` 列已存在。

## 2. 现状

```go
// /auth/me 的 UserView
ID, TenantID, TenantCode, Username, Nickname, Avatar, DepartmentID, IsSuper, Roles

// 缺失：Email、Phone、DepartmentName

// PUT /auth/profile
Nickname string
Email    *string
Avatar   *string
// 缺失：Phone

// /auth/sessions 的 Session
SID, UserID, CreatedAt, ExpiresAt
// 缺失：IP、UserAgent、current
```

会话存在 Redis（无 SQL 表），因此 IP/UA 需要在**登录时写入会话载荷**。

## 3. 改动

### 3.1 `/auth/me`

`data.user` 增加：

```
email            string | null
phone            string | null
dept_name        string | null   -- 部门名称，联表一次查询
```

规则：

- 只返回当前用户自己的资料，不返回他人
- `email`/`phone` 未设置时返回 `null`（不要空字符串）
- 一次 JOIN 取部门名，禁止 N+1

### 3.2 `PUT /auth/profile` 支持手机号

```
phone  *string   -- 可选；传入则更新，传空字符串表示清除
```

校验：

- E.164 格式（与登录标识一致），不合法 → 400/10001
- 租户内唯一冲突 → 409/10004，文案「该手机号已被使用」
- 变更写审计（`system:user:profile`），不记录明文以外的敏感信息（手机号本身可记）

> 邮箱变更沿用现有逻辑，不改语义。

### 3.3 `/auth/sessions` 补字段

`Session` 增加：

```
ip          string | null
user_agent  string | null   （截断 256）
current     bool            -- 是否为当前请求所用会话
```

实现：

1. 登录/刷新令牌时把 `IP`、`User-Agent` 写入会话载荷（Redis）
2. `OwnSessions` 读取并返回
3. `current` 由当前 JWT 的 `sid` 与行内 `sid` 比较得出
4. **旧会话没有这些字段** → 返回 `null` / `false`，不得报错

## 4. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 资料字段 | `/auth/me` 返回 email/phone/dept_name，与库一致 |
| 2 | 空值 | 未设置的邮箱/手机返回 `null` |
| 3 | 部门名 | 一次查询取回（SQL 计数守卫：资料查询 ≤2 条 SQL） |
| 4 | 改手机 | PUT 合法 E.164 → 200，重新登录后 `/auth/me` 为新值 |
| 5 | 非法手机 | `12345` → 400/10001 |
| 6 | 唯一冲突 | 与同租户他人重复 → 409/10004 |
| 7 | 清除手机 | 传空字符串 → 该字段变 `null` |
| 8 | 会话字段 | 登录后 `/auth/sessions` 返回 ip/ua，且当前会话 `current=true` |
| 9 | 多会话 | 两个设备登录，各自列表里只有自己那条 `current=true` |
| 10 | 旧会话兼容 | 人为构造缺字段的会话记录 → 返回 null/false，不报错 |
| 11 | 隐私 | 会话接口不返回 token、refresh、密码哈希 |
| 12 | 契约 | OpenAPI 更新，双向一致 |
| 13 | 全量 | `verify` 通过 |

## 5. 不做

- 会话强制下线的管理员视图（已有 `/admin/sessions`）
- 设备指纹、地理位置
- 头像上传（依赖文件管理模块）
- 邮箱变更的二次验证（P2）

## 6. Codex prompt

```
实现 docs/tasks/BE-IDN-003-profile-sessions.md。

分支：feat/be-idn-003-profile-sessions（从最新 main 开）

必读：本卡、internal/modules/identity/profile.go（UserView）、
internal/modules/identity/account_profile.go、account_sessions.go、
internal/modules/identity/auth_session.go（会话写入）、
internal/modules/identity/repo/（Session 与 OwnSessions）、
api/openapi/identity.yaml

改动：
1. /auth/me 增加 email / phone / dept_name（一次 JOIN，null 语义）
2. PUT /auth/profile 支持 phone（E.164、唯一冲突 409、清除语义、审计）
3. /auth/sessions 增加 ip / user_agent / current；登录时写入会话载荷
4. 旧会话缺字段返回 null/false，不报错

约束：
- 不返回 token / refresh / 密码哈希
- 不改邮箱与登录标识语义
- 会话 IP/UA 截断到合理长度

验收按卡片 §4 十三条逐条贴证据。
完成后停下报告，不要做前端。
```

## 7. 实现与验收记录

### 7.1 接口清单

| 方法 | 接口 | 本卡补全 |
|---|---|---|
| GET | `/api/v1/auth/me` | `email` / `phone` / `dept_name` 显式 null；同租户部门 LEFT JOIN |
| PUT | `/api/v1/auth/profile` | `phone` 沿用登录归一化；省略/null 保留、空串清除；400/10001、409/10004；事务内审计 |
| GET | `/api/v1/auth/sessions` | `ip` / `user_agent` / `current`；只读本人有效会话族；分页与安全字段投影 |

### 7.2 已确认的存储偏差

2026-11-14 用户确认：沿用基线 `f506751` 的 SQL 会话存储。实际持久化表为 `sys_refresh_token`，已有 `ip inet` / `user_agent text` 字段；Redis 保持撤销标记职责。本卡 §2 / §3.3 的 Redis 会话描述不适用于该基线，不新增存储或迁移。

登录与刷新统一在签发处约束会话元数据：IP 先限制输入长度再校验、规范化；User-Agent 截断至不超过 256 字节的有效 UTF-8。列表读取最新有效刷新记录的元数据，按 JWT sid 计算 `current`，历史缺字段输出 null。管理员会话投影保持原契约。

### 7.3 §4 逐条证据

以下命令在本卡分支执行，测试中的手机号和凭据均在运行时生成，不输出明文：

```powershell
go test ./internal/modules/identity ./internal/bootstrap -run 'ProfilePhone|ProfileFields|SessionMetadata' -count=1 -v
go test ./internal/bootstrap -run '^TestAdminRouteOpenAPIAndPermissionsAreExactlyEqual$' -count=1 -v
```

| # | 结果 | 测试与断言证据 |
|---|---|---|
| 1 | PASS | `TestProfileFieldsIntegrationDatabaseAndNulls`：当前用户 email/phone 与库一致，部门名为所属部门名称 |
| 2 | PASS | 同上：SQL NULL 与历史空字符串均输出显式 null |
| 3 | PASS | `TestProfileFieldsIntegrationOneJoinQuery`：SQL count = 1（预算 ≤2），JOIN 含部门 ID 与 tenant 条件 |
| 4 | PASS | `TestProfilePhoneIntegrationUpdateReloginClearAndAudit`：PUT 200，使用新手机号重新登录后 `/me` 为新值；用户名保留 |
| 5 | PASS | `TestProfilePhoneIntegrationInvalidAndDuplicate`：`12345` 返回 400/10001 |
| 6 | PASS | 同上：同租户重复返回 409/10004、指定文案；更新回滚，无成功审计 |
| 7 | PASS | `TestProfilePhoneIntegrationUpdateReloginClearAndAudit`：空串写入 SQL NULL，`/me` 输出 null；省略/null 保留原值 |
| 8 | PASS | `TestSessionMetadataIntegrationTwoDevicesAndRefresh`：登录 IP/UA 与请求一致，当前 sid 为 true；刷新更新元数据且 UA ≤256 字节 |
| 9 | PASS | 同上：两设备各自列表只有一个 `current=true`，对应各自 JWT sid |
| 10 | PASS | `TestSessionMetadataIntegrationLegacyOwnershipAndPagination`：人为省略 IP/UA 的记录返回 null/null/false，HTTP 200 |
| 11 | PASS | 同上：每行精确只有 sid/user_id/created_at/expires_at/ip/user_agent/current；他人会话不出现 |
| 12 | PASS | `TestProfileFieldsOpenAPIContract`、`TestProfileFieldsOpenAPIIntegrationResponses`、`TestAdminRouteOpenAPIAndPermissionsAreExactlyEqual`：结构双向一致、真实 HTTP 响应一致、路由/权限精确一致 |
| 13 | PASS | `scripts/verify.ps1 -Integration -Migration` 退出码 0，最后一行为 `all checks passed`；迁移 29 → 28 → 29，均 `dirty=false` |

补充回归：`TestProfilePhoneAuditRollbackAndPostCommitInvalidation` 验证审计失败回滚及提交后失效；缓存集成测试验证旧投影升级与暖缓存更新；`TestSessionMetadataLoginAndRefreshBounded` 验证非法/超长 IP、IPv6 规范化与 UTF-8 截断。

### 7.4 完成自检

本机没有 `make`，使用仓库 `verify.ps1` 执行相同的 lint / test 命令，并额外执行 vet、独立集成测试和迁移验证。最终验证的摘要输出如下：

```text
$ powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1 -Integration -Migration
== go vet ==
== golangci-lint ==
== go test ==
ok github.com/omwk/go-admin-api/internal/bootstrap 396.096s
ok github.com/omwk/go-admin-api/internal/modules/identity 19.702s
== integration ==
ok github.com/omwk/go-admin-api/internal/kernel/db 20.158s
ok github.com/omwk/go-admin-api/internal/bootstrap 370.370s
ok github.com/omwk/go-admin-api/cmd/cli 21.214s
== migrate up/down/up ==
version=29 dirty=false
version=28 dirty=false
version=29 dirty=false
all checks passed
```

- [x] lint：`golangci-lint run` 无诊断，退出码 0（`verify` 随后进入 `go test`）。
- [x] 单元及全量测试：`go test ./...` 通过；独立集成测试再次通过，摘要见上。
- [x] 迁移：`go run ./cmd/cli migrate up` / `down` / `up` 均通过，未增加或修改迁移文件。
- [x] 接口清单：见 §7.1，OpenAPI 与真实路由、请求/响应契约测试通过。
- [x] 无空实现：`rg -n 'TODO|not implemented' internal/` 无匹配。
- [x] 敏感信息：逐项检查新增 JSON 投影、审计字段与日志调用；会话精确字段集及审计禁止凭据字段断言通过，无新增敏感日志或明文凭据/手机号夹具。
- [x] 代码规模：用临时 Go AST 检查器执行 `go run (Join-Path $env:TEMP 'BE-IDN-003-limits.go') -- $changedGo`；本卡 Go 文件最大 194 行、函数最大 52 行、新增/修改函数最大圈复杂度 11。
- [x] CI 分片守卫：`powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-test-groups.ps1` 输出 `Source 512 = shards 511 + excluded 1` 和 `PASS: every integration test has exactly one shard or an explicit exclusion.`；本卡 9 个新增集成测试均归属 core。
- [x] `git diff --check` 通过；未改前端、未新增依赖或 schema，未提交、推送或创建 PR。
