# BE-IDN-006 · 超管无角色导致的数据范围拒绝

- ID: BE-IDN-006
- 类型: 修复
- 仓库: 后端
- 状态: 待实现
- 预计: 1 人日
- 分支: `feat/be-idn-006-super-scope`
- 依赖: BE-IDN-002（首用户规则）、BE-IDN-004（自助注册）

---

## 1. 现象

新租户的首个用户（`is_super=true`，但**没有任何角色**）登录后：

```
GET /admin/notifications        → 403 / 20108「超出数据权限」
GET /admin/notifications/unread-count → 同上
```

`/auth/me` 显示 `is_super=true`、`permissions` 118 项——权限是全的，
但**数据范围解析失败**，导致收件箱、以及任何走 `shopScope` 的接口都不可用。

已实测复现（把普通用户置为 `is_super=true` 且不授角色，登录后立即复现）。

## 2. 根因

`internal/bootstrap/credential_notification_store.go` 的 `notificationQuery`：

```go
if !p.User.IsSuper && !slices.Contains(p.Permissions, permission.ShopCredentialRotate) {
    return q.Where("n.credential_id IS NULL"), cancel, nil
}
human, err := newAdminStore(...).Authorize(ctx, tenant, user)  // ← 这里
shops, err := shopScope(human, model.Shop{}, ...)              // → 20108
```

超管走的是**第二条分支**（需要按店铺过滤），而 `Authorize`/`shopScope` 的
数据范围来自角色；无角色 → `ErrDataScopeDenied`。

两个独立缺陷：

1. **收件箱不应对超管做店铺范围过滤**：收件箱本就按 `target=user:<id>` 隔离，
   超管应看到自己全部通知
2. **首用户只设了 `is_super`，没授予内置 `super_admin` 角色**：导致依赖角色的
   逻辑（菜单、数据范围、部分 UI）全部落空

## 3. 改动

### 3.1 收件箱：超管短路

```go
if p.User.IsSuper {
    return q, cancel, nil      // 超管不做店铺过滤
}
if !slices.Contains(p.Permissions, permission.ShopCredentialRotate) {
    return q.Where("n.credential_id IS NULL"), cancel, nil
}
...
```

### 3.2 首用户同时授予内置超管角色

在**已有的首用户事务**内（BE-IDN-002 的 `AcceptInvite` 与 BE-IDN-004 的
`VerifyRegistration`），当判定「该租户无 active 超管 → 本用户成为超管」时，
同时插入 `sys_user_role` 绑定到内置 `super_admin` 角色：

- 幂等：已有绑定则跳过
- 与 `is_super` 同时设置，二者不替代彼此
- 不改变已有超管的角色集合（只处理「首个超管」这条路径）

### 3.3 数据范围：超管兜底

`shopScope` 与 `AdminStore.Authorize` 在 actor `is_super=true` 时返回「全部范围」，
不再依赖角色。这是纵深防御：即使某个调用点忘了短路，超管也不会被误拒。

## 4. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 收件箱 | `is_super=true` 且无角色的用户 → `GET /notifications` 200 |
| 2 | 未读数 | `unread-count` 200 |
| 3 | 首用户角色 | 邀请首用户成为超管后，`sys_user_role` 含 `super_admin` |
| 4 | 自助注册首用户 | 同上（注册路径） |
| 5 | 菜单 | 首用户 `/auth/menus` 返回非空菜单 |
| 6 | 数据范围 | 超管调用 `shopScope` 相关接口不再 20108 |
| 7 | 非超管不受影响 | 无角色普通用户仍返回「无权限」/收件箱不含店铺通知 |
| 8 | 幂等 | 同一首用户重复接受邀请，不会产生重复角色绑定 |
| 9 | 现有超管不变 | 已有超管的角色集合不因本改动增加 |
| 10 | 全量 | `verify` 通过 |

## 5. 不做

- 重构整个数据范围体系
- 给所有 is_super 用户自动补角色（只处理首用户路径）

## 6. Codex prompt

```
实现 docs/tasks/BE-IDN-006-super-scope.md。

分支：feat/be-idn-006-super-scope（从最新 main 开）

必读：本卡、internal/bootstrap/credential_notification_store.go（notificationQuery）、
internal/modules/identity/account_tenant_invite.go（首用户事务）、
account_verify.go（注册首用户）、internal/modules/shop/scope/、
internal/bootstrap/identity_tenant_recovery.go

改动：
1. notificationQuery：is_super 直接返回，不做店铺过滤
2. 首用户事务内授予内置 super_admin 角色（幂等，邀请与注册两条路径）
3. shopScope / Authorize 对 is_super 返回全部范围（兜底）

约束：
- 不改变非超管的范围语义
- 不给非首用户补角色
- 角色绑定在同一事务内

验收按卡片 §4 十条逐条贴证据。
完成后停下报告，不要做前端。
```
