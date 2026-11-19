# BE-IDN-019 · 通知路由与个人渠道

- ID: BE-IDN-019
- 类型: 新增
- 仓库: 后端
- 状态: 已实现（本地验收通过）
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

## 6. 本地验收记录（2026-11-19）

分支 `feat/be-idn-019-notification-routing`，基于 `3dc3b2fdfe864f1da1de118968cb98f3fe3bf34e`。

### 6.1 接口与实现

| 方法 | 路径（前缀 `/api/v1`） | 结果 |
|---|---|---|
| GET | `/auth/notification-channels` | 200；仅本人渠道、掩码和验证状态 |
| PUT | `/auth/notification-channels/{channel}` | 200；`endpoint` 必填、`enabled` 可选；保存后重置验证状态 |
| DELETE | `/auth/notification-channels/{channel}` | 204；按当前租户与用户删除 |
| POST | `/auth/notification-channels/{channel}/test` | 200；实际 POST `{title,body}`，成功后置 `verified_at` |
| GET / PUT | `/auth/notification-preferences` | 200；仅 email/webhook/bark/gotify，返回 `available/reason` |

写接口支持 `Idempotency-Key`；未知个人渠道或非法 URL 返回 400，未认证返回 401。
个人目的地使用现有 sealer，AAD 绑定 tenant/user/channel；outbox 只引用用户与目的地版本。
HTTP 传输限制为公网 HTTPS、最长 2048 字节、5 秒超时；保存与发送均检查 DNS，实际拨号固定到重新验证的公网 IP，禁用代理与重定向。
可通过 `notification.{webhook,bark,gotify}_allowed_hosts` 配置主机白名单。
账号邮件仍使用原 email 投递路径。

### 6.2 十三条证据

以下测试均 PASS，实际 HTTP 响应同时按 OpenAPI 验证：

| # | 证据 |
|---|---|
| 1 | `TestIdentityNotificationPersonalCRUDIntegration`：webhook/bark/gotify 保存、读取、删除为 200/204；`endpoint_enc` 为 bytea 密文，使用本人 AAD 可解密；幂等重放无重复发送。 |
| 2 | 同上：列表返回 `https://personal.example.invalid/…/****`，保存、列表、投递响应与审计/幂等/outbox/投递存储均不含完整 endpoint。 |
| 3 | `TestIdentityNotificationPersonalSSRFIntegration`：内网、回环、IPv6、云元数据、HTTP、超长 URL 全部 400；`TestPersonalTransportRejectsDNSRebinding/RejectsRedirects` 验证 DNS 重绑定与重定向均拒绝。 |
| 4 | CRUD 测试中，本地假 HTTP 服务实际收到 3 次 POST，Content-Type 为 application/json，正文精确为 title/body；成功后验证时间非空。失败探测不验证，更换 URL 后旧探测也不能验证新地址。 |
| 5 | `TestIdentityNotificationPersonalRoutingIsolationIntegration`：A 保存并测试验证 Bark 后，普通通知 `sent/bark`；本地假 HTTP 服务收到 A 的随机 URL 路径与 A 的消息正文。 |
| 6 | `TestAdminNotificationRobotRoutingIntegration`：`target=user:*` 的 wecom/dingtalk/sms 持久化为 `skipped/not_personal_channel`，attempts=0、HTTP 请求数=0；机器人 scope=personal 被拒绝。 |
| 7 | 同上：2 名用户的公告选择 wecom 后返回 201，仅生成 1 次租户机器人 POST，投递 sent，scope=system。 |
| 8 | `TestIdentityNotificationPersonalPreferencesIntegration`：`not_configured → not_verified → available → disabled`；缺邮箱为 `missing_email`；偏好不提供 SMS 或机器人，保存 SMS 偏好被拒绝。 |
| 9 | `TestIdentityNotificationPersonalFallbackIntegration`：排队后关闭 Bark，以及关闭 Bark 偏好两种情况，均回退并实际发送 SMTP；邮箱收到 2 次，HTTP=0。 |
| 10 | 路由隔离测试：本地假 HTTP 服务逐次断言 A→A 的随机路径、B→B 的随机路径；把 A 的密文放入 B 行后 `recipient_unavailable`，异租户读取失败，均无额外 HTTP 请求。 |
| 11 | `TestIdentityNotificationRoutingMigrationIntegration`：35→down 34（表不存在）→up 35 clean，endpoint_enc=bytea。另通过全链 up、回滚到底、再次 up、并发启动与结构不变量测试。 |
| 12 | `TestSystemNotificationRoutingOpenAPI`：个人输入/输出 JSON 字段双向一致，endpoint 为 writeOnly/maxLength=2048，响应不声明 endpoint；路由契约 `actual=63 manifest=63 OpenAPI=63`。 |
| 13 | `powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1`：go vet、golangci-lint、全仓库 go test 通过；末行 `all checks passed`，bootstrap 为 `ok ... 409.141s`。 |

可重现命令：

```powershell
go test -count=1 -v ./internal/bootstrap -run 'TestIdentityNotification(Personal|RoutingMigration)|TestAdminNotificationRobotRouting|TestSystemNotificationRoutingOpenAPI|TestSystemOpenAPIRoutesAndPermissionsMatch'
# PASS; ok github.com/omwk/go-admin-api/internal/bootstrap 3.267s
go test -count=1 -v ./internal/kernel/notifier/channel -run Personal
# PASS; ok github.com/omwk/go-admin-api/internal/kernel/notifier/channel 1.116s
go test -count=1 -v ./internal/kernel/db -run 'TestMigrateIntegration|TestMigrationIntegration'
# PASS; ok github.com/omwk/go-admin-api/internal/kernel/db 7.702s
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1
# all checks passed
make lint
# golangci-lint run; exit 0
make test
# go test ./...; exit 0; ok github.com/omwk/go-admin-api/internal/bootstrap (cached)
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-test-groups.ps1
# Source 611 = shards 610 + excluded 1
# PASS: every integration test has exactly one shard or an explicit exclusion.
rg -n 'TODO|not implemented' internal/
# 0 matches
go run (Join-Path $env:TEMP 'be-idn-019-size.go')
# Changed Go files=44, functions=212, max file=301/400, max function=64/80
git diff --check
# exit 0
```

补充验证：`TestAuditEventsRegistry` 通过（92 个登记事件）；租户清理实际删除 `sys_user_channel=1`，删除后为 0，平台/其他租户/全局记录指纹不变。
对改动 Go 文件使用 `go/parser` AST 扫描：44 个文件、212 个函数，最大文件 301/400 行，最大函数 64/80 行；脚本位于 `$env:TEMP/be-idn-019-size.go`。
隐私检查同时扫描响应与 channel_provider、notify_outbox、notify_delivery、notification、audit_log、idempotency_key 的 JSON 行内容，断言无生成的完整目的地、邮箱或手机号；生产路径错误使用固定安全文案，不记录 URL、上游错误或响应正文。

已清理：上述 `go test` 使用的隔离数据库 clone/schema 在 `t.Cleanup` 中自动删除，测试退出 0；开发库未写入本卡测试数据，未修改开发库系统配置，无保留验收数据。

偏差说明：无。审计事件登记、租户清理登记及原有迁移/通知回归测试随本卡维护；未增加第三方依赖。
