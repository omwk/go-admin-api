# BE-IDN-005 · 会话下线模式

- ID: BE-IDN-005
- 类型: 新增
- 仓库: 后端
- 状态: 待实现
- 预计: 0.2 人日
- 分支: `feat/be-idn-005-logout-others`
- 依赖: BE-IDN-003（会话元数据）

---

## 1. 背景

现在只有两种下线方式：

```
DELETE /auth/sessions/{sid}   单条
POST   /auth/logout-all       全部（含当前）
```

缺「保留当前、下线其他」——用户怀疑别处被盗用时最常用的动作。

底层已具备条件：`revokeSessions(tx, user, keep string)` 的 `keep` 参数就是保留的 sid，
只是 `logout-all` 传了空串。

## 2. 改动

新增一个接口：

```
POST /auth/logout-others
权限：登录即可
响应：{ "revoked": 3 }     // 被下线的会话数
```

行为：

- `keep = 当前 JWT 的 sid`
- 撤销该用户其余所有会话（含已过期的自然失效）
- 当前会话不受影响，token 继续可用
- 幂等：没有其他会话时返回 `revoked: 0`

`logout-all` 保持现状（全部下线，含当前）。

## 3. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 保留下线 | 3 台设备登录，调用后当前仍可用，另两台立即失效 |
| 2 | 计数 | 返回 `revoked=2` |
| 3 | 幂等 | 再次调用返回 `revoked=0` |
| 4 | 单条不受影响 | `DELETE /auth/sessions/{sid}` 行为不变 |
| 5 | 全部下线不变 | `logout-all` 仍踢掉当前会话 |
| 6 | 跨用户隔离 | 只影响自己，不影响同租户他人会话 |
| 7 | 审计 | 写审计 `system:user:logout_others`，含撤销数量，不含 sid 明文 |
| 8 | 契约 | OpenAPI 更新 |
| 9 | 全量 | `verify` 通过 |

## 4. Codex prompt

```
实现 docs/tasks/BE-IDN-005-logout-others.md。

分支：feat/be-idn-005-logout-others（从最新 main 开）

必读：本卡、internal/modules/identity/account_sessions.go（revokeSessions 的 keep 参数）、
account_self_http.go（路由注册）、api/openapi/identity.yaml

改动：
1. POST /auth/logout-others：keep = 当前 claims.SID，撤销其余会话
2. 返回 revoked 数量；幂等返回 0
3. 写审计（数量，不含 sid 明文）
4. OpenAPI 更新

约束：不改 logout-all 与单条下线的语义。

验收按卡片 §3 九条逐条贴证据。完成后停下报告，不要做前端。
```
