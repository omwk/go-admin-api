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
