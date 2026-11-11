# F1 · 账号生命周期：邀请、找回与自助

- 类型：平台层（框架可用性的核心）
- 预计：**6 人日**
- 分支：`feat/f1-account`
- 依赖：F3（邮件通道）；无邮件时邀请链接可由管理员复制转达

---

## 1. 背景

系统目前只有「管理员建号 + 管理员重置密码」。员工忘记密码的唯一路径是找同事帮忙。
这是框架层最基础的缺口。

## 2. 数据模型

```
sys_user 增加
  phone                text null          -- E.164
  email                text null
  password_changed_at  timestamptz
  force_password_change boolean default false
  totp_secret          text null          -- 预留，不启用

唯一约束（部分索引，忽略 null）
  (tenant_id, username)
  (tenant_id, phone)
  (tenant_id, email)

password_history(user_id, tenant_id, password_hash, created_at)
  保留最近 5 条

auth_token(
  id, tenant_id, user_id null, purpose(invite|reset|verify),
  token_hash, expires_at, used_at, attempts, created_by, created_at
)
  邀请与找回共用；令牌 32 字节，只存 sha256
```

## 3. 接口

### 邀请

```
POST   /admin/invites           生成邀请（预置 dept/roles/username/expires/uses）
GET    /admin/invites           列表（状态、已用次数、过期时间）
DELETE /admin/invites/{id}      撤销
POST   /auth/invite/accept      匿名：{ token, username?, password }
```

### 找回密码

```
POST /auth/password/forgot     匿名：{ account }（用户名/手机/邮箱）
POST /auth/password/reset      匿名：{ token, newPassword }
```

### 自助

```
PUT  /auth/password            { oldPassword, newPassword }  成功后撤销其他会话
GET  /auth/sessions            我的会话列表
DELETE /auth/sessions/{sid}    下线某个会话
POST /auth/logout-all          全部下线
PUT  /auth/profile             { nickname, email?, avatar? }
```

## 4. 关键规则

### 4.1 登录标识

- 登录请求的 `account` 按格式判断：含 `@` → 邮箱；以 `+`/纯数字 → 手机号；否则用户名
- **三种标识共用同一失败计数**（按 user 维度），防止换标识绕过锁定
- 手机号存 E.164；输入允许本地格式但需归一化后再比对

### 4.2 令牌

- 32 字节随机，base64url；库中只存 sha256
- 邀请默认 7 天、单次；`max_uses=0` 表示不限（批量入职）
- 重置令牌 30 分钟、单次
- 达到 `max_uses` 或用过后立即失效
- 失败一律返回统一文案「邀请或链接无效」，不区分不存在/过期/已用

### 4.3 密码策略

- 最小 8 位，须含字母与数字
- 不能与最近 5 次相同
- 90 天有效期：登录时若 `password_changed_at` 超期 → 返回 `password_expired`，只允许改密与登出
- 管理员重置后置 `force_password_change=true`；邀请注册的用户为 false（密码本人设的）

### 4.4 会话撤销

- 改密成功 → 撤销**其他**会话（保留当前）
- `logout-all` → 撤销全部（含当前）

### 4.5 匿名接口不返回真实链接（生产）

匿名 `POST /auth/password/forgot`：

- 所有环境统一 **202 + { accepted: true }**，账号不存在也返回同样形状与耗时
- **仅 app.env=dev|test** 额外返回 `dev_reset_url`；账号不存在时返回同样形状但**无效**的链接
- 生产环境**永不**返回链接，只经 F3 投递
- 链接是否出现**取决于环境，不取决于通道是否配置**——否则可用来推断账号存在性或服务端配置
- 生产未配置通道时：写 WARN 日志（含 request_id）、给租户管理员发站内通知、
  记指标 `password_reset_channel_unavailable`；请求方仍得 202

管理员侧提供 `POST /admin/users/{id}/reset-link`（权限 `system:user:reset`）：

- 生成一次性重置令牌并返回链接，**任何环境都返回**（管理员已认证且受审计）
- 审计记录操作者/目标/时间；令牌本身不入审计、不入日志
- 用途：生产未配邮件时，由管理员手动转达

### 4.6 防滥用

- `forgot` 接口：无论账号是否存在都返回 202，避免账号枚举
- 按 IP + 账号双维度限流（如 10 分钟 5 次）
- 邀请接受按 IP 限流，防止批量撞库

## 5. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 邀请注册 | 生成→接受→直接登录；用户落到预置部门与角色 |
| 2 | 邀请失效 | 单次令牌用第二次被拒；过期令牌被拒；撤销后立即失效 |
| 3 | 批量邀请 | `max_uses=3` 可注册 3 人，第 4 次被拒 |
| 4 | 忘记密码 | 提交任意账号均 202；邮件（或返回链接）可用 |
| 5 | 重置密码 | 令牌单次；重置后旧密码失效、新密码可登录 |
| 6 | 改自己密码 | 旧密码错 → 拒绝；成功后其他会话被踢、当前会话保留 |
| 7 | 密码策略 | 少于 8 位、纯字母、复用最近 5 次 → 全部拒绝 |
| 8 | 密码过期 | 把 `password_changed_at` 改为 91 天前 → 登录返回 `password_expired` |
| 9 | 登录标识 | 用户名/手机/邮箱都能登录；三种标识共用失败计数 |
| 10 | 账号枚举 | `forgot` 对不存在账号同样 202 |
| 11 | 我的会话 | 列出多个会话；删除其一后该会话失效 |
| 12 | 全量 | `verify -Integration -Migration` 通过 |

## 6. 不做

- 自助注册（无邀请码不可注册）
- MFA（预留字段）
- 邮箱变更的二次验证（P2）
- 短信找回（通道已具备，场景后置）

## 7. Codex prompt

```
实现 docs/tasks/F1-account.md。

必读：本卡、docs/design/admin-framework.md §7/§8、
internal/modules/identity/（认证与会话）、
internal/kernel/notifier/（发送通道，F3 已实现）、
docs/api/local-dev.md（本地账号与端口）

范围：
1. 迁移：sys_user 新列与唯一索引、password_history、auth_token
2. 邀请：生成/列表/撤销/接受
3. 找回：forgot / reset
4. 自助：改密、改资料、我的会话、logout-all
5. 登录标识支持用户名/手机/邮箱，失败计数按 user 统一
6. 密码策略（长度/复杂度/复用/90 天）

约束：
- 令牌只存哈希；过期为单次
- 所有失败统一文案，禁止账号枚举
- 改密后撤销其他会话
- 邮件通过 notifier 发送；未配置通道时返回可复制的链接并记录 outbox

验收按卡片 §5 十二条逐条贴证据。
完成后停下报告。
```

## 8. 实现接口说明

以下路径均带 `/api/v1` 前缀，响应沿用统一 envelope；204 无响应体。匿名接受、找回、重置支持可选 `tenant_id`，缺省为现有登录入口租户 1；自助接口从已验证 JWT 获取租户和用户。

| 方法 / 路径 | 请求 / 权限 | 成功响应 |
|---|---|---|
| `POST /admin/invites` | `dept_id`、`role_ids`、可选 `username` / `expires_at` / `max_uses`；`system:user:create`，复用部门范围与角色授予校验 | 201，邀请元数据和创建时可复制的 `link` |
| `GET /admin/invites` | `page=1&page_size=20`，上限 100；`system:user:list`；普通管理员仅看自己生成的邀请 | 200，`list/total/page/page_size`，含状态、已用次数、到期时间，不返回哈希或原始链接 |
| `DELETE /admin/invites/{id}` | `system:user:delete`；普通管理员仅能撤销自己生成的邀请 | 204，立即失效 |
| `POST /auth/invite/accept` | `token`、`password`、可选 `username` / `email` / `phone` / `tenant_id`；无预设用户名时至少提供一种标识 | 201，`id/username/nickname`；使用次数与建号在同一事务提交 |
| `POST /auth/password/forgot` | `account`、可选 `tenant_id`；IP 与归一化账号分别限流 | 202，所有环境均含 `accepted:true`；仅开发/测试额外返回 `dev_reset_url`，不存在账号的链接不可消费 |
| `POST /auth/password/reset` | `token/newPassword`、可选 `tenant_id` | 204，消费令牌并撤销全部历史会话和其他重置令牌 |
| `POST /admin/users/{id}/reset-link` | JWT、`system:user:reset`；复用用户管理范围校验 | 201，任何环境均返回一次性 `link`；审计仅记录操作者、目标、时间及令牌 ID |
| `PUT /auth/password` | JWT；`oldPassword/newPassword` | 204，保留当前会话、撤销其他会话 |
| `PUT /auth/profile` | JWT；`nickname`、可选 `email/avatar`；邮箱空串清空、缺省保留 | 200，安全用户资料；资料缓存失效待重试时带 `X-Authz-Invalidation: pending` |
| `GET /auth/sessions` | JWT；分页同上，仅自己的活动会话 | 200，`list/total/page/page_size`，会话不包含 refresh token、IP 或 user-agent |
| `DELETE /auth/sessions/{sid}` | JWT；只允许自己的会话 | 204；其他用户会话返回 404 |
| `POST /auth/logout-all` | JWT | 204，全部会话撤销，含当前会话 |

- 登录接受 `account`，兼容旧的 `username` 字段。密码超期时仍签发受限会话，响应含 `password_expired=true`、`password_change_required=true`、`password_status=password_expired`；管理员建号/重置为 `password_change_required`。受限会话只允许改密、登出、全部登出，其他认证接口返回 403 / 20102。
- 复用现有错误码：密码策略拒绝为 400 / 10001；无效、过期、撤销、已用链接统一 400 / 10001「邀请或链接无效」；限流为 429 / 10005。未新增错误码或第三方依赖。
- 邮件通过 F3 通道和 `notify_outbox` 投递。经确认，`auth_token` 仅存 SHA-256，outbox 沿用 F3 加密封装保存令牌 ID 与原始链接；复制链接、首次投递和重试均使用同一令牌，不替换哈希。发送前校验令牌摘要、用途、收件人、有效期及消费状态；已消费/过期/撤销令牌不会再发送。未配置加密密钥的本地环境仅保留安全引用，管理员复制链接仍可用，配置密钥后需重新签发以恢复邮件投递。
- 邀请创建接口始终向有权限的管理员返回链接；F3 通过创建者已有邮箱投递该邀请，供其转达。邀请接受者的联系方式不作为公开通知目标持久化。
- 无通道时邀请返回链接并记 `channel_unavailable`。找回请求事务内入队，匿名响应统一 202 和 `accepted:true`，并以公共响应时间下限补齐常规查无账号与入队路径的时差。是否出现 `dev_reset_url` 仅取决于 `app.env=dev/test`；生产无通道时同步记 `channel_unavailable`、含 request_id 的 WARN、租户管理员站内通知和 `password_reset_channel_unavailable` 指标，请求方不获得链接。生产人工转达使用受权限与审计保护的管理员 reset-link 接口。
- `app.public_url` / `CH_APP_PUBLIC_URL` 配置前端公开基址；链接含 `tenant_id` 查询参数，令牌置于 fragment。未配置基址时返回站内相对路径，启用邮件前应配置公开基址。API 接口仍通过 JSON 接收令牌，不记录完整链接。
- 原有 `phone/email` 列与用户名部分唯一索引复用；新增手机及大小写无关邮箱部分唯一索引，软删账号不占用标识。未修改既有迁移。

最终十二条验收映射、命令输出与偏差说明见 [F1-account-evidence.md](F1-account-evidence.md)。
