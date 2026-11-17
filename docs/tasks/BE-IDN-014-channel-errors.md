# BE-IDN-014 · 通知通道配置错误可诊断

- ID: BE-IDN-014
- 类型: 修复
- 仓库: 后端
- 状态: 待实现
- 预计: 0.5 人日
- 分支: `feat/be-idn-014-channel-errors`
- 依赖: 无

---

## 1. 现象

创建 SMTP 通道失败，只返回：

```json
{ "code": 10001, "message": "通道配置无效" }
```

用户无法知道错在哪。实测三种情况：

| 输入 | 实际原因 | 返回 |
|---|---|---|
| `smtp_addr=smtp.163.com`（无端口） | `net.SplitHostPort` 失败 | 通道配置无效 |
| `:465` + `password` 但无 `username` | 认证信息不完整 | 通道配置无效 |
| 完整配置 | — | 201 |

根因：`notification_management.go:122` 把 `channel.ValidateConfig` 的具体错误丢弃了：

```go
if err := channel.ValidateConfig(in.Channel, in.Kind, in.Config); err != nil {
    return errs.New(10001, 400, "通道配置无效")
}
```

而校验器内部其实有明确原因（`invalid SMTP endpoint`、`SMTP authentication incomplete` 等）。

## 2. 改动

### 2.1 校验错误带字段

`kernel/notifier/channel` 的校验改为返回结构化错误：

```go
type ConfigError struct {
    Field  string // smtp_addr / username / from_address / endpoint / ...
    Reason string // 用户可读的中文原因
}
```

覆盖全部 kind：

| kind | 校验点 |
|---|---|
| `smtp` | smtp_addr 需 host:port 且端口 1–65535；tls_mode 枚举；username/password 必须成对；from_address 合法 |
| `sendgrid` | api_key 非空；from_address 合法 |
| `aliyun_dm` | access_key_id/secret、region、from_address |
| `aliyun_sms` | access_key_id/secret、region、sign_name、template_code |
| `http` | endpoint 为 http(s) URL，不含 userinfo/fragment；secret 非空 |
| `wecom_bot` / `dingtalk_bot` | endpoint 合法 |

### 2.2 响应带 details

```json
{
  "code": 10001,
  "message": "SMTP 地址需包含端口，例如 smtp.163.com:465",
  "details": { "field": "smtp_addr", "reason": "需要 host:port" }
}
```

- `message` 取该字段的具体原因（中文）
- `details.field` 供前端定位输入框
- 校验器的原始英文错误不外泄（用户看不懂，且可能含内部信息）

### 2.3 常见原因文案

| 情况 | message |
|---|---|
| smtp_addr 无端口 | SMTP 地址需包含端口，例如 smtp.163.com:465 |
| 端口非法 | SMTP 端口需在 1–65535 之间 |
| 只有密码无用户名 | 已填写密码时必须同时填写 SMTP 用户名 |
| 只有用户名无密码 | 已填写用户名时必须同时填写 SMTP 密码 |
| tls_mode 非法 | TLS 模式只能是 tls、starttls 或 plain |
| 明文连非回环地址 | 明文连接仅允许本机地址，请改用 TLS |
| from_address 非法 | 发件人邮箱格式不正确 |
| endpoint 非法 | 服务地址必须是 http(s) URL |
| 缺少必填 | 缺少必填配置：<字段名> |

## 3. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 无端口 | 400 + message 提到端口 + `details.field=smtp_addr` |
| 2 | 只有密码 | 400 + message 提到用户名 + `details.field=username` |
| 3 | 只有用户名 | 400 + message 提到密码 |
| 4 | 端口越界 | 400 + 端口范围文案 |
| 5 | 非法 from_address | 400 + 发件人邮箱格式文案 |
| 6 | 非法 endpoint（http kind） | 400 + 服务地址文案 |
| 7 | 合法配置 | 201/200，行为不变 |
| 8 | 不漏内部信息 | 响应中不出现 `invalid SMTP endpoint` 等英文内部错误 |
| 9 | 其他 kind | sendgrid / aliyun_dm / aliyun_sms / 机器人各一条用例 |
| 10 | 契约 | OpenAPI 的 400 响应增加 details 说明 |
| 11 | 全量 | `verify` 通过 |

## 4. 不做

- 通道连通性测试逻辑（已有 `/providers/:id/test`）
- 前端表单（另卡）

## 5. Codex prompt

```
实现 docs/tasks/BE-IDN-014-channel-errors.md。

分支：feat/be-idn-014-channel-errors（从最新 main 9901c9e 开）

必读：本卡、internal/kernel/notifier/channel/config.go、
internal/bootstrap/notification_management.go（SaveProvider 与 ValidateConfig 调用处）、
internal/kernel/errs/、api/openapi/notification.yaml

改动：
1. channel 校验返回 ConfigError{Field, Reason}，覆盖全部 kind
2. SaveProvider 映射为 400 + 具体 message + details.field
3. 错误文案按卡片 §2.3，不泄露英文内部错误
4. OpenAPI 400 响应补 details

约束：
- 合法配置的行为与状态码不变
- 不改变 /providers/:id/test 语义

验收按卡片 §3 十一条逐条贴证据，第 1、2 条贴请求与响应原文。
完成后停下报告，不要做前端。
```
