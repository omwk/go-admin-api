# F3 · 通知通道：多服务商与投递日志

- 类型：平台层（账号生命周期与业务通知的共同前置）
- 预计：**5 人日**
- 分支：`feat/f3-channels`
- 依赖：无（F1 依赖本卡）

---

## 1. 目标

把现在只有 env 配置、单一实现的邮件发送，做成**可配置、可切换、可观测**的通道层。

已确认的产品决策：

- 通道：邮件 / 短信 / 企业微信 / 钉钉 / Webhook **都要支持**
- 允许不配置；配置了也可以单独停用
- 邮件首批：**SMTP + 阿里云 DirectMail + SendGrid**，其余按接口扩展

## 2. 数据模型

```
channel_provider
  id / tenant_id(可空=全局) / channel / kind / name
  config jsonb（加密） / enabled / priority / created_at / updated_at
  唯一：(tenant_id, channel, name)

notify_template
  id / tenant_id(可空) / code / locale / subject / body / format / enabled
  唯一：(tenant_id, code, locale)

notify_delivery
  id / tenant_id / channel / template_code / recipient_hash / recipient_domain
  provider_id / status(pending|sent|failed|skipped) / attempts /
  last_error / created_at / sent_at / next_retry_at

notify_outbox
  id / tenant_id / channel / template_code / payload jsonb / status
  —— 通道不可用时的「待投递」记录
```

## 3. 接口

```
GET    /admin/notifications/providers
POST   /admin/notifications/providers
PUT    /admin/notifications/providers/{id}
DELETE /admin/notifications/providers/{id}
POST   /admin/notifications/providers/{id}/test

GET    /admin/notifications/templates
PUT    /admin/notifications/templates/{code}

GET    /admin/notifications/deliveries
POST   /admin/notifications/deliveries/{id}/retry
```

## 4. 实现要点

### 4.1 适配器

```
type Sender interface {
    Channel() string
    Kind() string
    Send(ctx context.Context, msg Message) error
    Test(ctx context.Context) error
}
```

首批实现：

| channel | kind | 说明 |
|---|---|---|
| email | smtp | 通用兜底，TLS/STARTTLS |
| email | aliyun_dm | AccessKey + Region + 发信地址 |
| email | sendgrid | API Key |
| sms | aliyun_sms | 模板 + 签名 |
| wecom | wecom_bot | 群机器人 Webhook |
| dingtalk | dingtalk_bot | 群机器人 |
| webhook | http | 通用 POST，带签名 |

**其余（mailgun/tencent_ses/resend/aws_ses）同一接口注册即可，不在本轮。**

### 4.2 选择与回退

```
按 priority 升序取 enabled 的 provider
→ 发送成功：记 sent
→ 失败：记 failed 并尝试下一个
→ 全部失败：status=failed + 站内通知管理员
→ 一个都没配置：status=skipped + 写 notify_outbox（channel_unavailable）
```

### 4.3 模板

- 变量白名单：`{{.Name}}` 形式，禁止表达式求值
- 渲染失败 → 投递失败并记录原因，不发送半成品
- 租户模板覆盖全局模板

### 4.4 安全

- `config` 用现有 Sealer 加密，AAD 绑 `tenant:provider:kind`
- 接口返回的 config **必须脱敏**（只回显非密钥字段 + 「已配置」标记）
- 测试接口限流（每租户每分钟 3 次），防止被当发信机
- 收件人只存 `hash + domain`，不存完整地址
- 日志不得出现完整邮箱、手机号、Webhook URL 的 query

## 5. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 未配置通道 | 业务不失败；写 outbox 并标 `channel_unavailable` |
| 2 | 配置 SMTP | 测试接口发送成功；delivery 记 sent |
| 3 | 停用通道 | 启用开关关闭后不发信，记 skipped |
| 4 | 回退 | 主 provider 失败 → 次 provider 成功；attempts=2 |
| 5 | 全部失败 | 记 failed + 管理员站内通知 |
| 6 | 模板渲染 | 未知变量渲染失败，不发送半成品 |
| 7 | 脱敏 | 列表接口不含密钥；只回显「已配置」 |
| 8 | 收件人 | 库中无完整邮箱/手机号，只有 hash + domain |
| 9 | 限流 | 测试接口第 4 次返回 429 |
| 10 | 全量 | `verify -Integration -Migration` 通过 |

## 6. 不做

- 短信/企微/钉钉的真实商户接入联调（用 httptest 模拟）
- 模板可视化编辑器（只提供接口与默认模板）
- 营销邮件、退订、发送统计报表

## 7. Codex prompt

```
实现 docs/tasks/F3-channels.md。

必读：本卡、docs/design/admin-framework.md §3、
internal/kernel/notifier/（现有 email/webhook 实现）、
internal/modules/shop/credential（Sealer 用法）

范围：
1. 迁移：channel_provider / notify_template / notify_delivery / notify_outbox
2. Sender 接口 + 7 个实现（smtp / aliyun_dm / sendgrid / aliyun_sms / wecom_bot / dingtalk_bot / http）
3. 选择与回退、outbox、投递日志
4. 8 个管理接口（config 脱敏、测试限流）

约束：
- 未配置通道不得让业务失败
- config 加密存储、接口脱敏
- 收件人只存 hash + domain
- 外部服务全部用 httptest 模拟，不接真实服务商

验收按卡片 §5 十条逐条贴证据。
完成后停下报告，不要做 F1。
```

## 8. 实现说明

- 实际 API 前缀为 `/api/v1/admin/notifications`，按 §3 明确列出的 **9 条路由**交付（包括投递重试）；§7 的「8 个」为数量笔误。
- 管理接口使用现有 JWT 与 `system:setting:write` 权限；写入支持 `Idempotency-Key`，配置、模板与重试操作写审计。
- provider 默认属于当前租户；`global=true` 仅超级管理员可创建全局配置。更新时省略或留空的 config 字段保留已保存值，`channel/kind` 不可变更。
- config 使用现有 Sealer 加密，AAD 绑定租户、provider ID、kind；查询仅返回安全字段及 `*_configured` 标记。地址、密钥和完整 endpoint 均不回显。
- 测试收件人取当前操作者的已保存联系方式，不接受任意目标；每租户每分钟 3 次，多个 API 实例共用数据库原子计数。
- outbox 与调用方业务事务一起提交，worker 在事务外发送；通过领取租约避免并发投递，失效租约可恢复。未配置或全部停用时保留 `channel_unavailable` 记录。
- 收件地址在投递时读取；队列保存用户引用，变量在有密钥时一并加密。delivery 仅保存收件人 hash/domain；上游错误仅转换为固定失败原因。
- 同一通道存在新 provider 时使用新配置；没有新 provider 时兼容已有凭据通知设置。停用新 provider 不会转而使用旧设置发送。
- 默认 locale 为 `zh-CN`；模板管理允许 `Name`、`Platform`、`Severity`、`Path` 变量，拒绝表达式与未知变量。

各实现的必要 config（均不内置服务商 endpoint）：

| kind | 必要字段 |
|---|---|
| smtp | `smtp_addr`、`from_address`；可选 `username/password`、`tls_mode=tls/starttls` |
| aliyun_dm | `endpoint`、`access_key_id`、`access_key_secret`、`region`、`from_address` |
| sendgrid | `endpoint`、`api_key`、`from_address` |
| aliyun_sms | `endpoint`、`access_key_id`、`access_key_secret`、`region`、`sign_name`、`template_code` |
| wecom_bot | `endpoint` |
| dingtalk_bot | `endpoint`；可选签名 `secret` |
| http | `endpoint`、签名 `secret` |

HTTP 使用 HTTPS；仅本地 loopback 测试服务允许 HTTP/明文 SMTP。外部调用验收全部使用本地 `httptest` 服务。

## 9. 验收证据（2026-11-11）

定向命令：

```text
go test ./internal/bootstrap -run '^TestAdminNotification' -count=1 -v
PASS
ok github.com/omwk/go-admin-api/internal/bootstrap 5.366s

go test ./internal/kernel/notifier/channel -count=1
ok github.com/omwk/go-admin-api/internal/kernel/notifier/channel 1.190s
```

新增 12 个 F3 集成测试全部通过；传输测试覆盖 SMTP 的 TLS/STARTTLS 和另外 6 个 HTTP 实现的报文、签名、失败响应与取消。

| §5 # | 实测断言 | 证据测试（均 PASS） |
|---|---|---|
| 1 | 未配置时业务调用成功；delivery=`skipped`，outbox=`channel_unavailable` | `TestAdminNotificationIntegrationUnavailableAndDisabled`、`TestAdminNotificationIntegrationNoEncryptionConfigurationAndLeaseRecovery` |
| 2 | 测试接口通过本地 SMTP 实际收信；delivery=`sent`、attempts=1 | `TestAdminNotificationHTTPIntegrationSMTPRedactionRateAndIdempotency`、`TestSMTPEncryptedDelivery` |
| 3 | 停用后不发信，delivery=`skipped`；不回到旧设置发送 | `TestAdminNotificationIntegrationUnavailableAndDisabled`、`TestAdminNotificationIntegrationCredentialUsesManagedProvider` |
| 4 | 主 SendGrid 返回 503、次 SMTP 成功；attempts=2，provider_id 为次通道 | `TestAdminNotificationIntegrationFallbackFailureAndPrivacy` |
| 5 | 全部失败记 `failed`，管理员站内通知计数=1 | `TestAdminNotificationIntegrationFallbackFailureAndPrivacy` |
| 6 | 租户模板覆盖全局；未知变量导致 `template_render_failed`，外部调用次数=0 | `TestAdminNotificationIntegrationTemplatesAndTenantIsolation`、`TestTemplateRestrictedVariables` |
| 7 | 列表不含邮箱、密钥和 endpoint query；返回 `*_configured` 标记；AAD 替换不能解密 | `TestAdminNotificationHTTPIntegrationSMTPRedactionRateAndIdempotency`、`TestAdminNotificationHTTPIntegrationTemplateRetryDeleteAndAuthentication`、`TestProviderEncryptionBindsTrustedRelationships` |
| 8 | 扫描 provider/outbox/delivery/站内通知/审计/幂等记录，无生成的完整邮箱、手机号和密钥；hash=64 位，邮箱 domain 正确 | `TestAdminNotificationIntegrationFallbackFailureAndPrivacy`、`TestAdminNotificationIntegrationSMSHashAndRevokedRecipient` |
| 9 | 第 4 次测试返回 429；8 个并发实例仅放行 3 次，租户和分钟窗口独立 | `TestAdminNotificationHTTPIntegrationSMTPRedactionRateAndIdempotency`、`TestAdminNotificationIntegrationTestLimitAcrossManagersAndTenants` |
| 10 | vet、lint、全量单元与集成测试、迁移 up/down/up 全部通过 | `powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1 -Integration -Migration` 最后一行 `all checks passed` |

全量验收与其他检查结果：

```text
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1 -Integration -Migration
== go test ==
ok github.com/omwk/go-admin-api/internal/bootstrap 258.802s
== integration ==
ok github.com/omwk/go-admin-api/internal/kernel/db 6.475s
ok github.com/omwk/go-admin-api/internal/bootstrap 247.794s
ok github.com/omwk/go-admin-api/cmd/cli 11.801s
== migrate up/down/up ==
version=23 dirty=false
version=22 dirty=false
version=23 dirty=false
all checks passed

make lint
golangci-lint run
exit 0

make test
go test ./...
ok github.com/omwk/go-admin-api/internal/bootstrap (cached)
ok github.com/omwk/go-admin-api/internal/kernel/notifier/channel (cached)
exit 0

powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-test-groups.ps1
Source 391 = shards 390 + excluded 1
PASS: every integration test has exactly one shard or an explicit exclusion.
```

- 新增 12 个集成测试均归 core 分片；没有修改分片排除清单。
- `rg -n 'TODO|not implemented' internal` 无匹配；新增业务日志仅记录固定错误信息和 module/action，错误不会回传上游原始响应。
- 使用 Go parser 读取所有本次变更文件的行数与函数起止行：`Checked 26 Go files (file <=400, function <=80)`；`git diff --check` 通过。
- `GOCACHE`、`GOLANGCI_LINT_CACHE` 位于系统临时目录；没有新增依赖或改写既有迁移。
- 本机 `CGO_ENABLED=0`，未执行 race；CI race 结果需在合并前确认。
- 迁移命令由 verify 顺序执行：`go run ./cmd/cli migrate up`、`go run ./cmd/cli migrate down`、`go run ./cmd/cli migrate up`；全链 down 到底再 up 与并发启动测试也在全量集成测试中通过。

偏差说明：按 §3 的九条明确路由交付，未按 §7 的数量笔误省略重试接口；未开展 F1。
