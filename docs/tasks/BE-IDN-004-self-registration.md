# BE-IDN-004 · 可配置的租户自助注册

- ID: BE-IDN-004
- 类型: 新增
- 仓库: 后端
- 状态: 待实现
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
| 账号/邮箱已存在 | 409/10004，不区分「账号占用」还是「邮箱占用」的细节 |

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
| 11 | 邮箱重复 | 同租户同邮箱 → 409（不与账号冲突区分） |
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
- 已存在账号与不存在账号返回同一 202 形状
- 未配置邮件通道时沿用 F3 的降级（dev 返回链接，prod 只 202）
- 复用现有令牌表，不新增表

验收按卡片 §4 十五条逐条贴证据。
完成后停下报告，不要做前端。
```
