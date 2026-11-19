# BE-IDN-020 · 邮箱变更验证

- ID: BE-IDN-020
- 类型: 修复
- 仓库: 后端
- 状态: 后端已实现；真实收件正文核验待补（共享入口返回 401）
- 预计: 1 人日
- 分支: `feat/be-idn-020-email-change`
- 依赖: 无

---

## 1. 问题

`PUT /auth/profile` 允许**直接修改邮箱**，无需验证。

风险链：

```
会话被窃 → 改邮箱为攻击者地址 → 走"忘记密码" → 接管账号
```

邮箱是登录凭据与找回入口，改动必须证明「新邮箱属于本人」。

## 2. 改动

### 2.1 两步变更

```
POST /auth/profile/email/change   { "email": "new@acme.com" }
→ 202 { "pending": true, "expires_in_hours": 24 }
   向新邮箱发送验证链接；旧邮箱同时收到"邮箱变更申请"通知

POST /auth/profile/email/verify   { "token": "..." }
→ 200 { "email": "new@acme.com" }   变更生效
```

- 验证令牌：24 小时、单次、只存哈希（复用现有 token 机制，purpose=`email_change`）
- 链接：`<public_url>/auth/email/verify?tenant=<code>#token=...`（前端页转发到接口）
- 变更申请期间旧邮箱仍可登录
- 同一用户只能有一个待处理申请；新申请覆盖旧的并作废旧令牌
- 新邮箱若已被占用 → 409（不发送）

### 2.2 旧邮箱通知

向旧邮箱发送「你的账号正在变更邮箱」通知（模板 `identity.email_change`），
内容含时间、IP、UA 摘要与「如非本人操作请立即改密码」提示。

### 2.3 模板

新增 `identity.email_change`（变更成功）与 `identity.email_change_notice`（旧邮箱提醒）：

```
主题：{{.AppName}} 邮箱变更确认
{{.UserName}} 你好：请在 {{.ExpiresInHours}} 小时内点击链接确认将邮箱变更为 {{.NewEmail}}：
{{.Link}}
如非本人操作，请忽略本邮件并立即修改密码。
```

变量集合与 BE-IDN-016 一致，额外加 `NewEmail`。
内置模板变量声明与守卫测试同步更新。

## 3. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 申请 | 202，新邮箱收到验证邮件，旧邮箱收到提醒 |
| 2 | 验证 | 链接有效 → 200，`/auth/me` 显示新邮箱 |
| 3 | 旧邮箱 | 验证前旧邮箱仍可登录；验证后不可 |
| 4 | 令牌 | 24 小时过期；二次使用 400；新申请作废旧令牌 |
| 5 | 占用 | 新邮箱已存在 → 409，不发送 |
| 6 | 同邮箱 | 改成当前邮箱 → 400 |
| 7 | 隐私 | 日志/审计不含 token 与完整邮箱（掩码） |
| 8 | 模板 | 两条模板渲染通过；变量齐全守卫通过 |
| 9 | 契约 | OpenAPI 更新 |
| 10 | 全量 | `verify` 通过 |

## 4. 不做

- 手机号变更验证（无短信）
- 管理员强制改邮箱（已有管理端用户编辑）

## 4.1 实现与验收记录（2026-11-19）

| 方法 | 接口 | 成功响应 |
|---|---|---|
| POST | `/api/v1/auth/profile/email/change` | 202，`pending=true`、`expires_in_hours=24`，需要登录 |
| POST | `/api/v1/auth/profile/email/verify` | 200，`email`，仅凭验证令牌 |

十项逐条证据、检查输出和偏差见 [验收记录](evidence/BE-IDN-020/acceptance.md)。后端功能和全量验证通过；两封邮件真实送达并已核对收件列表，但共享正文接口返回 401「未授权」，第 1、3 条的收件正文核验尚未完成。

已清理：真实发信使用的三个隔离数据库自动销毁，`TestIdentityEmailChangeLiveCleanup` 复查剩余 0；开发库本卡临时用户/服务商为 0，未修改开发库数据或配置，见 `evidence/BE-IDN-020/cleanup.txt`。

## 5. Codex prompt

```
实现 docs/tasks/BE-IDN-020-email-change.md。

分支：feat/be-idn-020-email-change（从最新 main 开）

必读：本卡、internal/modules/identity/account_profile.go、account_invite.go（令牌机制）、
identity_account_notification.go（变量与模板）、
api/openapi/identity.yaml、模板种子

改动：
1. POST /auth/profile/email/change 与 /email/verify
2. 令牌 24h 单次、purpose=email_change，新申请作废旧令牌
3. 新邮箱验证邮件 + 旧邮箱提醒（两条新模板）
4. 占用/同邮箱校验
5. OpenAPI 同步；变量声明与守卫测试更新

约束：
- 不存 token 明文；日志与审计不含完整邮箱
- 未验证前不改 sys_user.email

验收按卡片 §3 十条逐条贴证据；第 1、3 条要真实发信并核对两封邮件。
完成后停下报告，不要做前端。
```
