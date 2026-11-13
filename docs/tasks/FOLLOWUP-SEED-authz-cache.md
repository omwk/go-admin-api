# FOLLOWUP-SEED · seed 后授权缓存未失效

- 类型：运维缺陷（升级流程会踩）
- 预计：**0.3 人日**
- 分支：`fix/seed-authz-cache`
- 依赖：无

---

## 1. 问题

升级说明写的是「重跑 `go run ./cmd/cli seed` 即可补权限与菜单」。
实际跑完后 **`/auth/menus` 不更新**：

```
sys_menu 已有 21 行（含通知中心/通知渠道/审计日志/登录历史）
super_admin 已有 19 个菜单授权、116 个权限
但 /auth/menus 只返回旧的 4 个项目、无 notify/audit 权限码
```

根因：授权缓存按版本号寻址。

```
authz:ver:<tenant>:<user>          版本号（授权接口变更时 INCR）
authz:menu:<tenant>:<user>:v<ver>  菜单负载
authz:perm:<tenant>:<user>:v<ver>  权限负载
```

**seed 直接写库，不 bump 版本号**，所以旧版本号对应的旧负载继续命中。
手动执行 `DEL authz:*` 后立即恢复正常（已实测）。

## 2. 修复

`cmd/cli seed` 在种子事务提交后，对**该租户的所有用户**递增版本号：

```go
// 事务外执行，失败只记警告，不阻断 seed
for _, userID := range tenantUserIDs {
    redis.INCR(authz.VersionKey(tenant, userID))
}
```

要点：

- 用 `INCR` 而不是 `DEL`：负载键按版本号命名，版本一换自然不再命中，旧键靠 TTL 过期
- 在**事务提交之后**执行：事务回滚时不应失效缓存
- 失效失败**不让 seed 失败**（否则缓存抖动会阻断升级），但要在输出里给出明确警告
- 只处理该租户的用户，不触碰其他租户

## 3. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 升级生效 | 新增权限与菜单后重跑 seed，`/auth/menus` 立即包含新项，无需手工清缓存 |
| 2 | 不影响密码 | 重跑后管理员密码不变，旧密码可登录 |
| 3 | 其他租户 | 租户 B 的缓存版本号不变 |
| 4 | 事务回滚 | 种子事务失败时，版本号不变（不失效） |
| 5 | 缓存不可用时 | Redis 不可用 → seed 仍成功，输出含警告 |
| 6 | 幂等 | 连续两次 seed，菜单与权限数量不变 |
| 7 | 全量 | `scripts/verify.ps1 -Integration -Migration` 通过 |

## 4. 文档

更新 `docs/api/local-dev.md` 与升级说明：

- 正常路径：重跑 seed 自动失效缓存
- 应急路径：`redis-cli --scan --pattern 'authz:*' | xargs redis-cli del`
  （仅当缓存失效失败时使用）

## 5. Codex prompt

```
实现 docs/tasks/FOLLOWUP-SEED-authz-cache.md。

必读：本卡、cmd/cli/main.go 的 seed 子命令、
internal/modules/identity/authz/cache.go（VersionKey 与版本语义）、
internal/bootstrap/identity_seed*.go（种子事务）

改动：
1. seed 事务提交后，对租户内所有用户 INCR authz 版本号
2. 失效失败只输出警告，不让 seed 失败
3. 补测试：升级后菜单立即生效；其他租户不受影响；事务回滚时不失效
4. 更新 local-dev.md 与升级说明

约束：
- 用 INCR 不用 DEL
- 事务外执行
- 只影响当前租户

验收按卡片 §3 七条逐条贴证据。
完成后停下报告。
```
