# 通知路由模型（设计决定）

- 日期：2026-11-19
- 状态：待实现（BE-IDN-019 / FE-IDN-014）
- 触发问题：渠道配置是否对所有人开放？企微机器人会不会把所有人的通知发到同一个群？

---

## 1. 结论

**渠道配置只有管理员能改**（`system:notify:manage`），这一点没问题。

**真正的缺陷是：把「传输通道」和「收件目的地」混成了一层。**

---

## 2. 现状为什么错

代码事实（`notification_store.go:56`）：

```go
if kind != "email" && kind != "sms" {
    return "", nil          // 企微 / 钉钉 / Webhook：没有收件人
}
```

于是：

| 渠道 | 谁是收件人 | 后果 |
|---|---|---|
| email | 用户自己的邮箱 | 正确 |
| sms | 用户自己的手机 | 正确 |
| wecom_bot | **无**，发到配置的群机器人 | **所有用户的通知都进同一个群** |
| dingtalk_bot | 同上 | 同上 |
| webhook | 同上 | 所有事件打到同一个地址 |

用户的问题完全成立：**「企微机器人配置成功后，所有用户的通知都发到管理员的企业微信机器人」——现在的实现就是这样。**

---

## 3. 正确模型：两层

```
传输层 Transport（租户级，管理员配置）
   SMTP 服务器 / 短信服务商 / 机器人 Webhook
   → 「用什么发」
        │
        ▼
目的地层 Destination（用户级，本人配置）
   邮箱 / 手机 / 个人 Webhook / Bark / Gotify
   → 「发给谁、发到哪」
```

### 3.1 渠道分工

| 渠道 | 传输层 | 目的地 | 用途 |
|---|---|---|---|
| email | SMTP 服务商 | 用户邮箱 | 个人通知、账号邮件 |
| webhook / bark / gotify | HTTP POST | **用户个人 URL** | 个人通知（自带 App 推送） |
| wecom_bot / dingtalk_bot | 机器人 Webhook | **群** | **仅系统告警与公告**，不参与个人路由 |

**短信不作为个人通知渠道**（2026-11-19 决定）：短信按条计费，成本不可控。
短信只保留给平台级运营场景（如后续的营销/告警），个人偏好页不提供。

### 3.2 硬约束

1. **机器人渠道不得用于个人通知**：投递时若 `channel ∈ {wecom, dingtalk}` 且 `target=user:*`，直接拒绝（`skipped/not_personal_channel`）。
2. **个人渠道必须有个人目的地**：用户未配置个人 webhook 时，`webhook` 渠道不可选。
3. **个人 URL 加密存储**：与 SMTP 密码同级（sealer），接口只返回掩码。
4. **降级规则**：用户偏好的渠道不可用时，回退到邮箱（账号类邮件不允许降级丢失）。

---

## 4. 数据模型

```
sys_user_channel
  id, tenant_id, user_id,
  channel        text  -- email | sms | webhook | bark | gotify
  endpoint_enc   bytea -- 加密的个人 URL（Bark/Gotify/Webhook）
  verified_at    timestamptz
  enabled        bool
  created_at, updated_at
  unique (tenant_id, user_id, channel)
```

- `email` / `sms` 的目的地仍取 `sys_user.email` / `phone`，不重复存
- `webhook` / `bark` / `gotify` 存加密 URL
- 新增渠道需要先「验证」：发送一条测试消息，点击/回调确认后 `verified_at` 置位

## 5. 接口

### 5.1 个人（本人）

```
GET    /auth/notification-channels          我的渠道与状态
PUT    /auth/notification-channels/webhook  { endpoint }   保存（加密）
POST   /auth/notification-channels/{id}/test                发送测试
DELETE /auth/notification-channels/{id}                     删除
```

### 5.2 偏好

`GET /auth/notification-preferences` 返回的渠道只包含：

- 邮箱（有 `sys_user.email`）
- 已配置并通过验证的个人渠道（webhook / bark / gotify）

**不含短信**。

未满足条件的渠道显示为「未配置」并给配置入口。

### 5.3 管理员

渠道配置（`/admin/notifications/providers`）不变，但：

- 机器人渠道的说明改为「用于系统告警与公告，不发送个人通知」
- 新增校验：机器人渠道不能设为 `enabled` 给个人路由用（配置项标注 `scope=system`）

## 6. 对已有功能的影响

| 功能 | 影响 |
|---|---|
| 注册验证 / 重置密码 | 仍走 email，用用户资料里的邮箱，不变 |
| 站内通知 | 不受影响 |
| 公告 | 可选用机器人渠道（群通知），符合定位 |
| 现有 `notification.test` | 仍走渠道测试，不变 |

## 7. 验收口径

1. 普通用户看不到渠道配置页（只有 `system:notify:manage` 可见）
2. 管理员配置企微机器人后，**个人通知不会进群**：投递记录显示 `skipped/not_personal_channel`
3. 用户在个人资料配置 Bark URL → 勾选「Bark 通知」→ 触发通知 → 手机收到
4. 未配置个人渠道时，偏好页对应项显示「未配置」并可跳转
5. 个人 URL 在数据库为密文，接口返回掩码
6. 公告可选机器人渠道并确实发到群

## 8. 不在本期

- 每个用户自己的 SMTP 账号（个人发件身份）
- 渠道优先级排序 UI（后端已支持 priority）
- 短信模板报备流程
