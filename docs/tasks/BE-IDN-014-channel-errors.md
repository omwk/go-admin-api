# BE-IDN-014 · 通知通道配置错误可诊断

- ID: BE-IDN-014
- 类型: 修复
- 仓库: 后端
- 状态: 已完成
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

分支：feat/be-idn-014-channel-errors（从 main 3211c0b 开）

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

## 6. 实施与验收记录

- 基线：`main` / `origin/main` 均为 `3211c0b7d911d750f217d99eccdb69a36d18c80f`，工作区干净后创建指定分支。
- [x] `channel/config.go` 全部校验分支返回 `ConfigError{Field, Reason}`，保留既有合法配置、安全限制和校验顺序。
- [x] `SaveProvider` 使用 `errors.As` 映射为 400/10001，返回中文 message、`details.field`、`details.reason`；未知错误使用安全兜底。
- [x] 新增表驱动单元测试、真实管理路由的 HTTP 集成测试、OpenAPI 契约测试。
- [x] 完整 `scripts/verify.ps1` 验收通过。

### 6.1 定向验收命令

```powershell
go test -count=1 -v ./internal/kernel/notifier/channel ./internal/bootstrap -run '^Test(ProviderConfig|AdminNotificationConfig)'
go test -count=1 -v ./internal/bootstrap -run '^TestAdminNotification(Config|HTTPIntegrationSMTPRedactionRateAndIdempotency)'
```

第二条命令输出：

```text
--- PASS: TestAdminNotificationConfigErrorMapping
--- PASS: TestAdminNotificationConfigOpenAPI
--- PASS: TestAdminNotificationConfigErrorsIntegration
--- PASS: TestAdminNotificationConfigValidIntegration
--- PASS: TestAdminNotificationHTTPIntegrationSMTPRedactionRateAndIdempotency
PASS
ok  github.com/omwk/go-admin-api/internal/bootstrap 1.481s
```

| # | 验收证据 |
|---|---|
| 1 | `ConfigErrorsIntegration/no_port` PASS；400；message 提到端口；`details.field=smtp_addr`。请求/响应原文见下。 |
| 2 | `ConfigErrorsIntegration/password_only` PASS；400；message 提到用户名；`details.field=username`。请求/响应原文见下。 |
| 3 | `ConfigErrorsIntegration/username_only` PASS；400；`已填写用户名时必须同时填写 SMTP 密码`；field=password。 |
| 4 | `ConfigErrorsIntegration/port_range` PASS；65536 返回 400 和 `SMTP 端口需在 1–65535 之间`；单测另覆盖 0、-1、空、非数字端口。 |
| 5 | `ConfigErrorsIntegration/from_address` PASS；400；`发件人邮箱格式不正确`。 |
| 6 | `ConfigErrorsIntegration/http_endpoint` PASS；400；`服务地址必须是 http(s) URL`；单测覆盖 userinfo、fragment、非法协议和非回环明文地址。 |
| 7 | `ConfigValidIntegration` 七种 kind 均 PASS；创建 201，更新 200；省略配置保留旧值，非法更新回滚；原有 SMTP 测试接口回归 PASS。 |
| 8 | 每条 HTTP 错误均断言不含英文内部错误、动态凭据与邮箱；`ConfigErrorMapping` 验证包裹错误及未知错误安全兜底；存储隐私断言 PASS。 |
| 9 | `ConfigErrorsIntegration` 的 sendgrid、aliyun_dm、aliyun_sms、wecom_bot、dingtalk_bot 子用例均 PASS；单测逐字段检查各 kind 必填项。 |
| 10 | `ConfigOpenAPI` PASS；POST/PUT 使用带 field/reason 的 400 契约，示例响应通过验证；测试接口维持既有契约。 |
| 11 | `powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1` 返回 0；go vet、golangci-lint、go test ./... 全部通过；最后一行 `all checks passed`。 |

### 6.2 第 1、2 项请求与响应原文

以下为隔离测试库的管理路由请求体及响应体原文；`********` 是测试实际提交的生成占位值，不是真实凭据。

```http
POST /api/v1/admin/notifications/providers
Content-Type: application/json

{"channel":"email","kind":"smtp","name":"no_port","config":{"from_address":"e4abb49a2521e6475d314a0a@example.invalid","smtp_addr":"smtp.example.invalid"},"enabled":true,"priority":0,"global":false}

HTTP/1.1 400
{"code":10001,"message":"SMTP 地址需包含端口，例如 smtp.163.com:465","details":{"field":"smtp_addr","reason":"SMTP 地址需包含端口，例如 smtp.163.com:465"},"trace_id":"identity-integration-trace"}
```

```http
POST /api/v1/admin/notifications/providers
Content-Type: application/json

{"channel":"email","kind":"smtp","name":"password_only","config":{"from_address":"e4abb49a2521e6475d314a0a@example.invalid","password":"********","smtp_addr":"smtp.example.invalid:465"},"enabled":true,"priority":0,"global":false}

HTTP/1.1 400
{"code":10001,"message":"已填写密码时必须同时填写 SMTP 用户名","details":{"field":"username","reason":"已填写密码时必须同时填写 SMTP 用户名"},"trace_id":"identity-integration-trace"}
```

### 6.3 迁移、分组与自检

```text
$ powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1
== go vet ==
== golangci-lint ==
== go test ==
ok  github.com/omwk/go-admin-api/internal/bootstrap 394.334s
ok  github.com/omwk/go-admin-api/internal/kernel/db 16.630s
ok  github.com/omwk/go-admin-api/internal/kernel/notifier/channel 0.350s
ok  github.com/omwk/go-admin-api/scripts/smoke (cached)
all checks passed

$ go test -count=1 -v ./internal/kernel/db -run '^TestMigrateIntegration'
--- PASS: TestMigrateIntegrationReversibleAndRepeatable (5.04s)
--- PASS: TestMigrateIntegrationConcurrentStartup (1.87s)
PASS
ok  github.com/omwk/go-admin-api/internal/kernel/db 7.747s

$ powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-test-groups.ps1
Source 581 = shards 580 + excluded 1
PASS: every integration test has exactly one shard or an explicit exclusion.
```

- 迁移测试在隔离库执行全链 up、逐步 down 到底、再次 up，以及并发启动；无迁移文件改动。
- `rg -n 'TODO|not implemented' internal/` 无匹配；`git diff --check` 通过。
- 使用 Go 标准库 `go/parser` 检查本卡五个 Go 文件：最大文件 259 行，最大函数 `SaveProvider` 62 行。
- 检查新增错误路径与日志调用：错误内容仅为固定中文和白名单字段；无新增生产日志；测试原文仅使用生成占位凭据；数据库隐私断言通过。
- 未新增接口；现有 POST/PUT 的错误响应已在 OpenAPI 和本卡记录。
- 已清理：本次验收使用 `testenv.Database` / `testenv.EmptyDatabase` 自动隔离并销毁测试库；未在开发库创建测试数据、审计或修改配置。

### 6.4 偏差说明

- 基线不存在 `api/openapi/notification.yaml`；通知路由及其既有契约测试实际使用 `api/openapi/system.yaml`，因此在该文件补充 POST/PUT 的 400 响应。
- 本机未安装 `make`；使用仓库 `verify` 脚本执行 Makefile 对应的 `golangci-lint run`、`go test ./...`，并额外执行 `go vet ./...`。
