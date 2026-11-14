# BE-IDN-002 · 租户管理员可恢复与首用户规则

- ID: BE-IDN-002
- 类型: 修复
- 仓库: 后端
- 状态: 待实现
- 预计: 2 人日
- 分支: `feat/be-idn-002-tenant-admin-recovery`
- 依赖: BE-IDN-001
- 来源: `docs/design/system-layer-gaps-v2.md` §2.A

---

## 1. 问题

`POST /admin/tenants` 会创建租户管理员（`is_super=true`，`status=disabled`）+ 7 天单次邀请，
但 `invite_url` **只在响应里出现一次**。

三条断链：

```
链接关闭即消失 → 无重发入口
平台管理员进不去他人租户建用户（跨租户 404，这是对的）
邀请过期或丢失 → 该租户永久无管理员，只能改库
```

实测：本地 3 个 SF-5 验收租户全部停用、无任何 active 用户。

## 2. 方案

### 2.1 重新签发管理员邀请

```
POST /admin/tenants/{id}/admin-invite
权限：system:tenant:manage（仅平台管理员可跨租户）
响应：{ "invite_url": "...", "expires_at": "..." }
```

规则：

| 情况 | 行为 |
|---|---|
| 租户无 active 超管 | 直接签发；旧的未使用令牌作废 |
| 租户已有 active 超管 | 409「该租户已有管理员」；`?force=true` 才签发并作废旧令牌 |
| 租户已停用 | 409「租户已停用」 |
| 平台租户 | 允许（等价于重置平台管理员，需二次确认由前端做） |

审计：`system:tenant:admin_invite`，含 actor_tenant / target_tenant / force 标记。

### 2.2 租户成员只读

```
GET /admin/tenants/{id}/members
权限：system:tenant:list（仅平台管理员）
返回：id / account / nickname / status / is_super / created_at
```

用途：平台管理员确认「这个租户到底有没有人能登录」，不必让前端猜。

**不返回**：密码哈希、会话、PII。

### 2.3 租户列表补充可用性字段

`GET /admin/tenants` 每项增加：

```
admin_active_count   int   该租户 active 状态超管数
member_count         int   该租户用户数
```

这样列表页可以直接显示「未激活」——回答「这个租户能不能用」。

### 2.4 首用户自动超管（为 BE-IDN-004 自助注册预留）

规则：租户内**第一个注册成功的用户**（无论邀请还是自助）若租户当前无 active 超管，
则自动 `is_super=true`。

实现位置：接受邀请 / 注册成功的事务内判断，而不是靠调用方传参。

## 3. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 重签发 | 对无管理员的租户签发 → 链接可用，登录后为 active 超管 |
| 2 | 旧令牌作废 | 重签发后旧链接返回「邀请无效」 |
| 3 | 已有管理员 | 已有 active 超管时签发 → 409；force=true 成功且旧令牌作废 |
| 4 | 停用租户 | 签发 → 409 |
| 5 | 审计 | 每次签发一条审计，含 actor_tenant 与 target_tenant |
| 6 | 成员列表 | 返回该租户用户，不含密码/会话；普通管理员调用 404 |
| 7 | 列表字段 | `admin_active_count` / `member_count` 与成员列表一致 |
| 8 | 首用户 | 空租户接受邀请后为超管；已有超管时新用户按邀请角色授予 |
| 9 | 隔离 | 平台管理员签发不影响其他租户 |
| 10 | 全量 | `verify` 通过 |

## 4. 不做

- 租户删除
- 平台管理员直接在该租户内创建普通用户（先用重签发 + 租户内管理员自建）
- 邀请邮件自动发送（依赖邮件通道，可后接）

## 5. Codex prompt

```
实现 docs/tasks/BE-IDN-002-tenant-admin-recovery.md。

必读：本卡、docs/design/system-layer-gaps-v2.md §2.A、
internal/modules/identity/tenant_provision.go（现有邀请逻辑）、
account_invite.go（邀请令牌）、identity_tenant_store.go、
api/openapi/system.yaml

改动：
1. POST /admin/tenants/{id}/admin-invite（支持 force，旧令牌作废，审计）
2. GET /admin/tenants/{id}/members（只读，平台管理员）
3. 租户列表增加 admin_active_count / member_count
4. 首用户自动超管：接受邀请事务内判断租户是否已有 active 超管

约束：
- 仅平台管理员可跨租户
- 成员列表不含密码哈希与会话
- 审计含 actor_tenant 与 target_tenant

验收按卡片 §3 十条逐条贴证据。
完成后停下报告，不要做前端。
```

