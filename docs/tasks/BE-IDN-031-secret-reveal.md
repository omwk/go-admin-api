# BE-IDN-031 · API 密钥可查看

- ID: BE-IDN-031
- 类型: 新增
- 仓库: 后端
- 状态: 待实现
- 预计: 0.5 人日
- 分支: `feat/be-idn-031-secret-reveal`
- 依赖: BE-IDN-029

---

## 1. 需求

当前密钥明文**只在创建/轮换时返回一次**，之后只能看前缀。用户认为体验不好，
要求管理员可以随时查看。

技术上可行：`api_client_secret` 存的是 **`cipher_text` + `nonce` + `key_version`**
（不是哈希），鉴权时本来就会解密比对签名。所以「查看」只是把已有能力暴露给管理员。

## 2. 改动

### 2.1 查看接口

```
GET /admin/api-clients/{id}/secrets/{sid}/reveal
→ { "secret": "<完整明文>", "prefix": "sk_abc", "expires_at": null }
```

要求：

- 权限：`gateway:client:manage`（不是 list）
- 响应头：`Cache-Control: no-store`
- 审计：每次查看写一条 `gateway.secret.reveal`（记录 client_id、secret_id、操作者、IP）
- 只能查看**本租户、状态 active、未过期**的密钥；其他返回 404
- 明文不进入日志、审计 before/after（审计只记 id）

### 2.2 列表增加可查看标记

`GET .../secrets` 每项增加：

```json
{ "id": 3, "secret_prefix": "sk_abc", "status": "active",
  "revealable": true, "expires_at": null, "last_used_at": "…" }
```

- `revealable=false` 表示已吊销/已过期（解密也不返回）

### 2.3 前端展示

- 列表行「查看」按钮 → 二次确认弹窗「将显示完整密钥，请勿在公共屏幕操作」→ 显示明文 + 复制
- 显示后 60 秒自动隐藏（前端行为）
- 不做「再次创建才能看」的限制

## 3. 安全取舍（写进文档，不写进代码）

| 风险 | 缓解 |
|---|---|
| 有权限者可批量导出密钥 | 审计每次查看；建议每租户每日查看次数上限（本卡实现：>20 次/日 返回 429） |
| 数据库泄露 | 密钥为密文 + AAD 绑定，仍需主密钥才能解开 |
| 屏幕泄露 | 前端 60 秒自动隐藏 + 复制提示 |

## 4. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 查看成功 | 返回明文与提交时一致 |
| 2 | 审计 | 每次查看新增一条 `gateway.secret.reveal`，不含明文 |
| 3 | 权限 | 只有 list 权限的账号 → 403 |
| 4 | 范围 | 跨租户 / 已吊销 / 已过期 → 404 |
| 5 | 限流 | 同一租户第 21 次查看 → 429 |
| 6 | 响应头 | `Cache-Control: no-store` |
| 7 | 列表标记 | active 为 true；吊销后变 false |
| 8 | 兼容 | 创建/轮换仍返回一次明文（行为不变） |
| 9 | 契约 | OpenAPI 更新，双向一致 |
| 10 | 全量 | `verify` 通过 |

## 5. 不做

- 查看前的二次密码验证（可用但增加摩擦，先不做）
- 密钥使用范围限制（后续）

## 6. Codex prompt

```
实现 docs/tasks/BE-IDN-031-secret-reveal.md。

分支：feat/be-idn-031-secret-reveal（从最新 main ea0d87e 开）

必读：本卡、internal/modules/gateway/secret.go（Seal/Open 与 secretAAD）、
auth.go（解密比对逻辑）、admin_http.go（secrets 路由）、
admin_types.go（SecretInfo）、permission/registry.go

改动：
1. GET /admin/api-clients/{id}/secrets/{sid}/reveal（明文 + no-store + 审计）
2. 列表增加 revealable
3. 每租户每日查看上限 20 次，超限 429
4. OpenAPI 同步

约束：
- 明文不进入日志与审计
- 仅 active 且未过期、同租户可查看
- 创建/轮换返回一次明文的行为不变

验收按卡片 §4 十条逐条贴证据：
- 第 2 条贴审计记录（证明无明文）
- 第 5 条贴第 21 次 429
完成后停下报告，不要做前端。
```
