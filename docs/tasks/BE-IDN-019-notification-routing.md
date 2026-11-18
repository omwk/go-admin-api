# BE-IDN-019 · 通知路由与个人渠道

- ID: BE-IDN-019
- 类型: 新增
- 仓库: 后端
- 状态: 待实现
- 预计: 2 人日
- 分支: `feat/be-idn-019-notification-routing`
- 依赖: BE-IDN-018（通知完整性）
- 依据: `docs/design/notification-routing.md`

---

## 1. 问题

`Recipient()` 对 `wecom/dingtalk/webhook` 返回空收件人，导致这些渠道把
**所有用户的通知都发到同一个群/地址**。

根因：缺少「个人目的地」概念——渠道被当成既是传输又是目的地。

## 2. 改动

### 2.1 新增个人渠道表

迁移 `000035`：

```sql
CREATE TABLE sys_user_channel (
  id bigserial PRIMARY KEY,
  tenant_id bigint NOT NULL REFERENCES tenant(id),
  user_id bigint NOT NULL REFERENCES sys_user(id),
  channel text NOT NULL CHECK (channel IN ('webhook','bark','gotify')),
  endpoint_enc bytea NOT NULL,
  verified_at timestamptz,
  enabled boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, user_id, channel)
);
```

- URL 用现有 sealer 加密（AAD 绑 tenant/user/channel）
- 列表只返回掩码（`https://api.day.app/…/****`）

### 2.2 个人渠道接口

```
GET    /auth/notification-channels
PUT    /auth/notification-channels/{channel}   { endpoint }
POST   /auth/notification-channels/{channel}/test
DELETE /auth/notification-channels/{channel}
```

校验：

| 渠道 | endpoint 规则 |
|---|---|
| `webhook` | https，禁止内网/回环地址（SSRF 防护），≤2048 |
| `bark` | https，host 为 `api.day.app` 或用户自建（可配置白名单），≤2048 |
| `gotify` | https，同上 |

测试发送：POST JSON `{"title","body"}`，超时 5 秒，成功返回 200。

### 2.3 路由规则

`Service.recipient` 改为：

| channel | recipient 来源 |
|---|---|
| email | `sys_user.email` |
| webhook / bark / gotify | 该用户 `sys_user_channel.endpoint_enc` 解密 |
| wecom / dingtalk | **不允许个人投递** |
| sms | 不参与个人通知（成本原因，见设计文档 §3.1） |

并在 `dispatch` 入口加硬约束：

```go
if (d.Channel == "wecom" || d.Channel == "dingtalk") && strings.HasPrefix(target, "user:") {
    d.Status, d.LastError = "skipped", "not_personal_channel"
}
```

### 2.4 偏好可用渠道

`GET /auth/notification-preferences` 的 channel 列表按用户实际能力生成：

- email：有邮箱
- webhook/bark/gotify：已配置且 `verified_at` 非空
- **不提供短信**（成本原因）
- 不可用渠道返回 `available:false` 与 `reason`

### 2.5 渠道配置页说明

- 机器人渠道（wecom/dingtalk）标注「系统告警与公告专用，不发送个人通知」
- provider 增加 `scope` 概念：`personal`（email/sms）与 `system`（机器人）
- 公告选择机器人渠道合法；个人投递使用机器人渠道一律跳过

## 3. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 个人渠道 CRUD | 保存/读取/删除成功，URL 入库为密文 |
| 2 | 掩码 | 列表返回掩码，不含完整 URL |
| 3 | SSRF | 内网/回环地址 → 400 |
| 4 | 测试发送 | 本地假服务收到 POST，返回成功 |
| 5 | 路由 | 用户配置 Bark 后，通知投递到该 URL（假服务断言收到） |
| 6 | 机器人约束 | `target=user:*` + `channel=wecom` → skipped/not_personal_channel |
| 7 | 公告 | 公告可选 wecom 渠道并成功投递 |
| 8 | 偏好 | 未配置个人渠道时 `available:false` 且 reason 正确 |
| 9 | 降级 | 用户关闭 Bark 后回退邮箱 |
| 10 | 隔离 | A 用户的 URL 不会用于 B 用户投递 |
| 11 | 迁移 | 000035 up/down 通过 |
| 12 | 契约 | OpenAPI 更新，双向一致 |
| 13 | 全量 | `verify` 通过 |

## 4. 不做

- 渠道优先级 UI
- 个人 SMTP
- 短信报备流程

## 5. Codex prompt

```
实现 docs/tasks/BE-IDN-019-notification-routing.md。

分支：feat/be-idn-019-notification-routing（从最新 main 开）

必读：本卡、docs/design/notification-routing.md、
internal/bootstrap/notification_store.go（Recipient）、
internal/kernel/notifier/service.go（recipient/dispatch）、
internal/kernel/notifier/readability.go、sealer 用法、
internal/modules/identity/account_self_http.go（自查接口挂载方式）

改动：
1. 迁移 000035：sys_user_channel（加密 endpoint）
2. 个人渠道 CRUD + 测试发送 + SSRF 防护
3. 路由：webhook/bark/gotify 取个人 endpoint；机器人渠道禁止个人投递
4. 偏好返回 available/reason
5. provider 增加 personal/system scope 校验
6. OpenAPI 同步

约束：
- 个人 URL 必须加密存储，任何响应不得回显完整 URL
- 不允许个人投递走机器人渠道
- 不改账号邮件（email）路径

验收按卡片 §3 十三条逐条贴证据；
第 5、10 条用本地假 HTTP 服务断言收到请求与目标用户。
完成后停下报告，不要做前端。
```
