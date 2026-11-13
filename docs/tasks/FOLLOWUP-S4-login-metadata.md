# FOLLOWUP-S4 · 登录审计来源信息补全

- 类型：缺陷修复（S4 交付后暴露）
- 预计：**0.3 人日**
- 分支：`fix/s4-login-metadata`
- 依赖：S4（已合并）

---

## 1. 问题

S4 交付了登录历史页，但真实登录产生的记录 **IP 与 User-Agent 都是空**。

原因：`auth_login.go` 的 `loginAudit` 构造 `audit.Event` 时没有带上 `meta.IP` / `meta.UserAgent`，
虽然 `Login(ctx, tenant, username, password, meta RequestMeta)` 已经收到这两个字段。

后果：安全排查时看不到「从哪台机器登录的」，登录历史页退化成一串时间戳。

## 2. 改动

| # | 文件 | 改动 |
|---|---|---|
| 1 | `internal/modules/identity/auth_login.go` | `loginAudit` 增加 `meta` 参数，写入 `IP` 与 `UserAgent` |
| 2 | 同上 | 三个调用点（限流拒绝、决策失败、决策成功）传入 `meta` |
| 3 | 测试 | 真实 HTTP 登录（成功 / 密码错 / 未知账号）各产生一条含 IP/UA 的审计 |

**不改表、不改迁移、不改查询接口。**

## 3. 约束

- 未知账号记录仍然 `resource_id=unknown`、显示「未知账号」
- **不得**把尝试输入的用户名或密码写入审计
- IP 取 `RequestMeta.IP`（已有解析逻辑），不要自己解析 header
- User-Agent 截断到合理长度（≤256 字节），避免超长 header 污染审计

## 4. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 成功登录 | 审计记录 IP 与 UA 非空且与请求一致 |
| 2 | 密码错误 | 同样记录 IP/UA，result=failure |
| 3 | 未知账号 | 记录 IP/UA，resource_id=unknown，不含输入的用户名 |
| 4 | 限流拒绝 | 被限流的尝试也写入 IP/UA |
| 5 | 超长 UA | 4000 字节 UA 被截断到 256，不报错 |
| 6 | 查询可见 | `GET /admin/login-logs` 返回上述 IP/UA |
| 7 | 无泄漏 | 审计中不含密码、不含未知账号的输入串 |
| 8 | 全量 | `scripts/verify.ps1 -Integration -Migration` 通过 |

## 5. 不做

- 地理位置解析（IP → 城市）
- 设备指纹
- 修改登录限流策略

## 6. Codex prompt

```
实现 docs/tasks/FOLLOWUP-S4-login-metadata.md。

必读：本卡、internal/modules/identity/auth_login.go、
internal/kernel/audit/（Event 结构与 IP/UA 字段）、
S4 的查询实现（internal/modules/system/audit.go）

改动：
1. loginAudit 增加 meta 参数，写入 IP 与 UserAgent（UA 截断 ≤256）
2. 三个调用点传入 meta
3. 测试：真实 HTTP 登录成功/密码错/未知账号各一条，断言 IP/UA 与请求一致
4. 未知账号仍 resource_id=unknown，不含输入的用户名或密码

约束：不改表、不改迁移、不改查询接口。

验收按卡片 §4 八条逐条贴证据。
完成后停下报告。
```
