# BE-IDN-015 · 投递记录可读与可操作

- ID: BE-IDN-015
- 类型: 修复
- 仓库: 后端
- 状态: 待实现
- 预计: 1 人日
- 分支: `feat/be-idn-015-delivery-readability`
- 依赖: 无

---

## 1. 现状

投递记录页显示的字段对管理员几乎没有信息量：

| 列 | 现在 | 问题 |
|---|---|---|
| 模板 | `identity.verify` | 模板代码，不是人看的中文名 |
| 收件人 | `hash: —` `域: —` | 只存了哈希；未发送时全空，管理员无法判断发给谁 |
| 状态 | `待发送` / `已发送` | 尚可，但缺失败/跳过 |
| 失败原因 | `message_unavailable` | 原始错误码，不是人话 |
| 操作 | 空 | 失败后无法重试 |

### 1.1 为什么会发生「邮件没发出去」

实测：本地只启动了 `cmd/api`，没起 `cmd/worker`。

```
notify_outbox: 49 条 pending
notify_delivery: 全部 pending，attempts=0
```

worker 启动后立刻消化：3 条 sent、51 条 failed（旧事件 payload 已过期，
报 `message_unavailable`）。

**这是启动流程缺陷，不是代码缺陷**——但暴露了可用性问题：
投递记录页没有「为什么卡住」的任何线索。

## 2. 改动

### 2.1 模板显示名

投递记录返回 `template_name`（join `notify_template` 按 `template_code`），
模板已删除时回退 `template_code`。

### 2.2 收件人：存掩码，显示掩码

现状只存 `recipient_hash` + `recipient_domain`，**不可逆，管理员无法核对**。
新增 `recipient_masked`（如 `a***@163.com`、`138****1234`）：

- 写入时按渠道生成：邮箱保留首字符与完整域名；手机保留前 3 后 4
- 列表返回 `recipient_masked`
- **不存明文**，也不提供查看明文接口（需要时走单独的合规流程）
- 旧记录该字段为空，前端显示 `—`

迁移 `000033`：`notify_delivery` 增加 `recipient_masked text`。

### 2.3 失败原因中文化

维护错误码 → 中文原因映射（`kernel/notifier` 内）：

| code | 中文 |
|---|---|
| `message_unavailable` | 消息内容已过期或模板已删除 |
| `recipient_unavailable` | 收件人无效或已退订 |
| `provider_delivery_failed` | 服务商返回失败 |
| `channel_unavailable` | 没有可用的发送渠道 |
| `provider_timeout` | 服务商响应超时 |
| `config_invalid` | 渠道配置无效 |
| 其他 | 显示原码 |

响应同时给 `last_error`（原码）与 `last_error_message`（中文）。

### 2.4 重试语义

现有 `POST /deliveries/{id}/retry` 只允许 `failed` / `skipped`。

补充：

- `pending` 且 `attempts=0` 且创建超过 5 分钟 → 允许「重新入队」
  （对应 worker 没启动的场景；已入队事件重投是幂等的）
- `sent` 不允许重试（避免重复发送）
- 批量重试：`POST /deliveries/retry` body `{ids:[...]}`，返回 `{retried, skipped}`

### 2.5 列表筛选

`GET /deliveries` 增加：

| 参数 | 说明 |
|---|---|
| `status` | pending / sent / failed / skipped |
| `channel` | email / sms / wecom … |
| `template_code` | 精确筛选 |

## 3. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 模板名 | 列表返回 `template_name`，中文名正确；模板删除后回退 code |
| 2 | 收件人掩码 | 邮箱显示 `a***@163.com`；手机显示 `138****1234` |
| 3 | 不存明文 | 数据库 `notify_delivery` 中无完整邮箱/手机（检查列与内容） |
| 4 | 失败原因 | 失败记录返回中文 `last_error_message`，原始码仍在 |
| 5 | 未知码 | 未知错误码原样显示，不空白 |
| 6 | 重试失败项 | failed → 200，状态回 pending，worker 处理后可用 |
| 7 | 重试 pending | 卡住 5 分钟以上的 pending 可重新入队；5 分钟内返回 409 |
| 8 | 不可重试 | sent 重试返回 409 |
| 9 | 批量重试 | 3 条混合状态 → `{retried:2, skipped:1}` |
| 10 | 筛选 | status/channel/template_code 各自与组合生效 |
| 11 | 迁移 | 000033 up/down 通过；旧行 `recipient_masked` 为 null |
| 12 | 契约 | OpenAPI 更新，双向一致 |
| 13 | 全量 | `verify` 通过 |

## 4. 不做

- 查看收件人明文（合规流程另开）
- 重发已成功的投递
- 投递记录导出

## 5. Codex prompt

```
实现 docs/tasks/BE-IDN-015-delivery-readability.md。

分支：feat/be-idn-015-delivery-readability（从最新 main 开）

必读：本卡、internal/kernel/notifier/channel/http.go（deliveries 路由）、
internal/kernel/notifier/handler.go（写入投递记录处）、
internal/kernel/notifier/config.go、internal/bootstrap/notification_store.go、
notification_commands.go（Retry）、migrations/000014_notification*.up.sql

改动：
1. 迁移 000033：notify_delivery.recipient_masked
2. 写入时生成掩码（邮箱首字符+域名；手机前3后4）；不存明文
3. 列表返回 template_name、recipient_masked、last_error_message
4. 错误码中文映射（表见卡片 §2.3），未知码回退原码
5. retry 扩展：pending 超 5 分钟可重新入队；sent 拒绝；新增批量重试
6. 列表增加 status/channel/template_code 筛选
7. OpenAPI 同步

约束：
- 任何响应都不得出现完整邮箱或手机号
- 不改变已成功投递的记录
- worker 不在本卡范围

验收按卡片 §3 十三条逐条贴证据，第 3 条要贴数据库查询证明无明文。
完成后停下报告，不要做前端。
```
