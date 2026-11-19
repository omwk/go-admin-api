# BE-IDN-020 验收记录

分支：`feat/be-idn-020-email-change`；基线：`dd05f4ca04e4b98cb8a17db11c26652b0b6e1e4c`。仅后端，未做前端；未提交、推送或创建 PR。

后端实现和自动验证通过。真实邮件均已送达两个用户指定收件箱；共享收件正文接口返回 401「未授权」，因此尚未完成第 1、3 条要求的实际邮件正文核对，不能标记整卡验收通过。

## 十条验收

| # | 状态 | 证据与实际结果 |
|---|---|---|
| 1 申请 | 部分完成 | `live-current.txt`：HTTP 202，pending=true、expires_in_hours=24；SMTP 两条 delivery 均 sent，收件地址分别来自待验证邮箱与原邮箱。真实收件列表：旧邮箱提醒 2026/11/19 10:54:28，新邮箱确认 10:54:29（Asia/Shanghai），见 mail-old.jpg / mail-new.jpg。正文接口 401，正文及收件链接未核对。`TestIdentityEmailChangeIntegrationRequestVerifyAndLogin` 已通过替身核对目标及模板内容。 |
| 2 验证 | 通过 | `live-current.txt`：使用加密 outbox 中的令牌调用匿名 verify，HTTP 200，GET /auth/me 返回新邮箱；聚焦集成同时校验响应符合 OpenAPI。令牌没有取自收到的邮件，不能视为实际收件链接验证。 |
| 3 旧邮箱 | 登录行为通过；收件正文待补 | `live-current.txt`：同一真实发信流程里，验证前旧邮箱 POST /auth/login=200，验证后旧邮箱=401、新邮箱=200；两封实际邮件的列表收件已核对，正文仍因 401 无法核对。 |
| 4 令牌 | 通过 | `acceptance.txt`：RequestVerifyAndLogin 二次使用=400、purpose=email_change 且摘要不等于原令牌、期限恰为 24h；ReplacedAndExpired 新申请撤销旧令牌、24h 边界=400；ConcurrentSingleUse 两个并发消费恰为一个 200、一个 400；ConcurrentRequests 两条请求仅一个未撤销令牌。 |
| 5 占用 | 通过 | ValidationAndProfileBypass：占用邮箱=409，notify_outbox/auth_token 均 0、实际投递调用 0；ConflictDuringVerification 验证时再次检查占用，409 后旧邮箱与可用令牌保持原值。 |
| 6 同邮箱 | 通过 | ValidationAndProfileBypass：当前邮箱及其大写形式均 400；无效/空邮箱均 400；PUT profile 直接换邮箱或清空=400，回传当前邮箱仍可更新昵称。 |
| 7 隐私 | 通过 | `live-current.txt`：捕获服务日志并逐行检查 audit_log、notify_delivery、notify_outbox，不含原令牌、完整旧/新邮箱；audit 仅写 email=***，投递仅存掩码，outbox 加密；AuthToken 联系字段 json:-。数据库 GORM logger 使用 Silent；人工检查新增错误、审计与日志调用，无令牌或邮箱插值。 |
| 8 模板 | 通过 | `acceptance.txt`：两个新模板、17 个种子模板全量渲染；TestAdminNotificationTemplateDeclarationsMatchProducers 对照真实生产者变量与声明；TestEmailChangeVariablesAndMandatorySecurityMail 检查变量、缺变量拒绝、安全邮件不可退订。旧邮箱通知不含验证链接。 |
| 9 契约 | 通过 | identity.yaml 已同步两个 POST、请求/响应、鉴权、400/409、24h、单次、fragment 链接和 PUT profile 限制；TestAdminRouteOpenAPIAndPermissionsAreExactlyEqual 保留双向路由一致性；TestIdentityOpenAPIContainsExactlyThirteenDocumentedAuthRoutes 检查两个新增路由及匿名 verify；HTTP 集成校验真实响应。 |
| 10 全量 | 通过 | `verify-final.txt`：go vet / golangci-lint / go test ./... / 显式集成全部通过，末行 all checks passed；bootstrap 单元/集成分别 403.670s / 391.463s，显式集成 db 9.368s / CLI 32.362s。 |

## 验收命令与输出

~~~powershell
go test ./internal/bootstrap ./internal/modules/identity ./internal/kernel/notifier/channel -run 'TestIdentityEmailChange|TestAdminNotificationTemplateDeclarationsMatchProducers|TestSeededTemplate|TestAccountLink|TestEmailChangeVariables' -count=1 -v
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1 -Integration
make lint
make test
go test ./internal/kernel/db -run 'TestMigrat.*Integration' -count=1 -v
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-test-groups.ps1
~~~

聚焦记录见 acceptance.txt。全量记录见 verify-final.txt。make lint 退出 0，最后一行 `golangci-lint run`；make test 退出 0，bootstrap 汇总为 `ok github.com/omwk/go-admin-api/internal/bootstrap 406.201s`，最后一行为 `ok github.com/omwk/go-admin-api/scripts/smoke (cached)`，原始输出见 make-test.txt。

迁移记录 migrations.txt：TestMigrateIntegrationReversibleAndRepeatable 按真实嵌入迁移执行 up / up / version、逐步 down 到 0，再 up 到 36；并发初始化、审计语义和关键结构不变量均通过，`ok .../internal/kernel/db 7.959s`。未修改既有迁移，仅新增 000036 up/down。

CI 分片守卫见 test-groups.txt：`Source 620 = shards 619 + excluded 1`，每个集成测试恰归一个分片；9 个新增 Identity 集成测试进入 core。

## 真实发信与访问限制

通过 Go overlay 临时验收测试调用真实 HTTP handler、数据库与通知服务；仅从开发库解密读取已配置 SMTP，复制进隔离测试数据库；发信通过既有 delivery/outbox 和真实 SMTP 服务商，不使用邮件替身。最新流程生成 1 条 email_change 令牌、2 条 delivery、8 条审计，随后隔离库自动销毁。临时 overlay 未进入源码或 CI。

2026-11-19 10:14、10:50 两次尝试均完成真实投递，但等待正文/列表人工核验的门闩超时，原始失败记录保留在 live.txt / live-final.txt。最新 live-current.txt 的 API/登录与隐私检查通过，明确记录正文未核验；没有将替身或 outbox 的核对当作实际收件正文证据。

共享网页实际点击两封邮件，分别请求 `/api/shared/<share-id>/messages/<message-id>`，均返回 HTTP 401，正文 `{"error":"未授权"}`。刷新后仍可看到收件列表，但无法读取正文。已请求可用的正文访问入口；未绕过第三方访问控制。

![旧邮箱真实收到申请提醒](mail-old.jpg)

![新邮箱真实收到确认邮件](mail-new.jpg)

## 自检与清理

- make lint、make test、最终 verify 结果见对应原始记录。
- 新增接口已记录在任务卡接口表；OpenAPI 双向守卫保留并同步新增两个操作。
- `git diff --check` 通过；`rg -n 'TODO|not implemented' internal/` 无命中。
- 新增服务/handler/store/通知代码人工检查：不含 SQL 在 handler、第三方依赖或明文令牌持久化；邮箱不进入日志/审计内容。
- 改动 Go 文件 AST 检查见 size.txt：files=25、max_file_lines=182、max_function_lines=71；检查命令 `go run <temporary-size-check.go> -- <git diff/ls-files 的全部改动 Go 文件>`，使用 go/parser 与 token.Position 计数。
- 已清理：三次真实发信隔离数据库均已自动销毁；独立 TestIdentityEmailChangeLiveCleanup 查询 pg_database 复查剩余 0；开发库本卡临时用户/服务商均为 0，见 cleanup.txt。开发库只读 SMTP 配置，无本卡审计噪声、业务数据或配置需要删除/恢复，因此无需对开发库运行 audit-prune。保留收件箱内验收邮件和本目录证据。
- 未运行 CI；本地结果不包含 CI race 结论。

## 偏差与待补

1. 实际邮件正文访问被收件服务 401 拒绝，待补实际正文/收到链接核对；第 1、3 条未完整签收。可用的正文入口到位后需要重新进行一次隔离真实发信流程，因既有隔离库和令牌已清理。
2. 卡片 §2.2 的旧邮箱模板代码与 §2.3/用户明确要求不一致，按用户要求使用 `identity.email_change` 发新邮箱确认、`identity.email_change_notice` 发旧邮箱提醒。
3. 卡片要求扩展令牌 purpose，新增迁移同时记录原/新邮箱作为验证绑定与定向通知来源，令牌仅存摘要；未改变 sys_user schema，未改既有迁移或设计文档。
4. 对既有三处守卫仅同步新增路由数和迁移链基线，保留精确双向比较及实际 000035 down/up 行为；没有删除、放宽行为断言。
