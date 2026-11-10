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

### 4.5 防滥用

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
