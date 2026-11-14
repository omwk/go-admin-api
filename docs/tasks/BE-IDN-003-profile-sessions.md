# BE-IDN-003 · 个人资料与会话的字段补全

- ID: BE-IDN-003
- 类型: 修复
- 仓库: 后端
- 状态: 待实现
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
