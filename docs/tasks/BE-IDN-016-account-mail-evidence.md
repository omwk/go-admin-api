# BE-IDN-016 · 账号邮件验收记录

- 分支：feat/be-idn-016-account-mail
- 起点：main 45960ad；实现与验收均在此分支进行。
- 验收日期：2026-11-18（Asia/Shanghai）
- 结论：任务卡 §4 的 11 项已验证；真实邮件已收到，正文 token 已打码，实际邮件地址打开验证页并激活成功。
- 范围：后端配置、账号链接、模板变量、模板 seed、设置、OpenAPI 与测试。无前端改动、无新增依赖、无迁移/表结构改动，注册验证语义保持原样。

## 1. 十一项验收证据

| # | 验收项 | 实际结果与证据 |
|---|---|---|
| 1 | dev 绝对链接 | TestPublicURLDevelopmentDefault 断言缺省值为 http://127.0.0.1:5555 且有 WARN；TestAccountLinkAbsoluteTenantAndFragment 覆盖 verify/reset/invite 的绝对地址。[输出](evidence/BE-IDN-016/config-links-settings.txt) |
| 2 | prod 强校验 | TestPublicURLProductionRequired、TestPublicURLRejectsInvalidBaseWithoutExposingIt 通过；真实 API 二进制以空 public_url 启动退出 1，报错 app.public_url is required in prod。[启动输出](evidence/BE-IDN-016/prod-start.txt) |
| 3 | 租户参数 | 三类链接测试同时断言 tenant=code 与 tenant_id=id；含特殊字符的 code 正确 URL 编码。真实邮件含 tenant=be-idn-016-mail 与 tenant_id=27。[测试](evidence/BE-IDN-016/config-links-settings.txt)、[邮件截图](evidence/BE-IDN-016/received-mail-redacted.jpg) |
| 4 | token 位置/隐私 | 链接测试断言 query 无 token、fragment 保存 token；真实注册检查加密 outbox、5 条审计/投递元数据及 3 份 API/worker 日志均无原始 token。仅在内存比较原值，输出不含原值。[真实检查](evidence/BE-IDN-016/live-privacy.txt)、[集成测试](evidence/BE-IDN-016/acceptance-current.txt) |
| 5 | 模板内容 | 14 条内置模板逐条渲染；identity 三条断言品牌、租户、独占行链接、一次性说明，SupportEmail 空/非空均覆盖；实际队列集成验证昵称回退、租户名、RFC3339 UTC 过期时间、邀请 168 小时与重置 30 分钟。[守卫](evidence/BE-IDN-016/guard-pass.txt)、[变量与隐私集成](evidence/BE-IDN-016/acceptance-current.txt)、[真实正文](evidence/BE-IDN-016/received-mail-redacted.jpg) |
| 6 | 变量齐全守卫 | TestSeededTemplatesRender 正常退出 0；overlay 故意为 notification.test 添加未提供的 MissingAccountMailVariable 后退出 1；撤去 overlay 后再次通过。[通过](evidence/BE-IDN-016/guard-pass.txt)、[故意失败](evidence/BE-IDN-016/guard-fail.txt) |
| 7 | seed 更新 | TestSeedAccountTemplatesIntegrationRefreshBuiltinsAndPreserveCustom 调用 SeedIdentity 两次，验证全部 14 条内置模板及租户内置覆盖的 subject/body/format 更新，enabled 状态保留；code=identity.custom 的整行完全不变。[输出](evidence/BE-IDN-016/acceptance-current.txt) |
| 8 | 真实注册/收件/激活 | 新租户 27 中创建账号，注册 HTTP 201；实际 SMTP delivery=98 sent；用户指定收件箱收到 22:05:06 邮件；打开收到的地址，页面显示“邮箱已验证，现在可以登录了”；数据库 user=115 active，auth_token=61 used_count=1，成功验证审计 1 条。[注册](evidence/BE-IDN-016/live-register.txt)、[发送](evidence/BE-IDN-016/live-deliver.txt)、[正文截图](evidence/BE-IDN-016/received-mail-redacted.jpg)、[成功截图](evidence/BE-IDN-016/verification-success.jpg)、[数据库核对](evidence/BE-IDN-016/live-verify.txt) |
| 9 | 重置/邀请可用 | 三类链接契约测试通过；真实 HTTP 集成 TestIdentityAccountInviteIntegrationPresetAndSingleUse、TestIdentityAccountRecoveryIntegrationUniformAcceptedAndReset、TestIdentityAccountAdminIntegrationResetLinkAndAudit 通过，覆盖接受邀请、设置新密码、单次消费及管理员重置链接。[输出](evidence/BE-IDN-016/links-usable.txt) |
| 10 | OpenAPI 契约 | account.yaml 补齐现有邀请/重置/找回接口文档；identity.yaml、system.yaml 中相关地址描述同步明确 absolute HTTP(S)、tenant=code、兼容 tenant_id 及 fragment。TestAccountMailAbsoluteLinkOpenAPI 通过。[输出](evidence/BE-IDN-016/acceptance-current.txt) |
| 11 | 全量 verify | scripts/verify.ps1 -Integration 退出 0，包含 go vet、golangci-lint、全包测试及 db/bootstrap/cli 集成测试，最后一行 all checks passed。[完整输出](evidence/BE-IDN-016/verify.txt) |

共享邮箱以纯文本展示本期 text 格式邮件，因此验收从实际收到的正文复制完整地址打开；未把发送端渲染结果当成收件证据。正文截图来自真实邮箱页面。截图只对 token 做像素遮盖，原始截图已从临时目录删除；验证成功截图的页面正文不含 token。

## 2. 关键改动文件

- internal/kernel/config/config.go、configs/config.example.yaml：dev 地址默认值与告警、prod 必填/绝对地址校验，品牌缺省值 CommerceHub。
- internal/modules/identity/account_link.go 及所有 AccountLink 调用方：绝对 URL、租户 code/id 兼容、fragment token、错误传播。
- internal/bootstrap/identity_account_notification.go：7 个业务变量始终提供，保留内部 TokenID；品牌、昵称、租户名称、TTL、UTC 到期时间及实时客服设置来自当前数据。
- internal/kernel/setting/defaults.go、reader.go：notification.support_email，空值合法且省略客服行。
- internal/bootstrap/notification_seed.go、identity_seed.go：统一 14 个内置清单，强制更新内置内容，保留启用状态与自定义 code。
- internal/kernel/notifier/channel/template.go：受限模板语法增加 SupportEmail 条件；保留 missingkey=error，并检查条件内所有变量。
- api/openapi/account.yaml、identity.yaml、system.yaml：现有账号链接接口文档。
- 配套单元/集成测试及最小测试配置适配。

reset 文案按任务卡 §3.4 使用“分钟”，因此兼容保留 ExpiresInHours 名称，reset 值为 30；verify 为 24，invite 默认 168（7 天）。未修改令牌实际有效期。

## 3. 验收命令与输出

本机使用 PowerShell 等价入口；make lint 对应 golangci-lint run，make test 对应 go test ./...，均由 verify 完整执行。构建/静态分析缓存置于系统临时目录，不在仓库内。

~~~powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1 -Integration
~~~

~~~text
== go vet ==
== golangci-lint ==
== go test ==
ok  github.com/omwk/go-admin-api/cmd/cli                  47.148s
ok  github.com/omwk/go-admin-api/internal/bootstrap     440.355s
== integration ==
ok  github.com/omwk/go-admin-api/internal/kernel/db       9.910s
ok  github.com/omwk/go-admin-api/internal/bootstrap     443.534s
ok  github.com/omwk/go-admin-api/cmd/cli                  43.798s
all checks passed
~~~

变量守卫：

~~~powershell
go test ./internal/bootstrap -run '^TestSeededTemplatesRender$' -count=1 -v
go test -overlay "$env:TEMP/be-idn-016-guard-overlay.json" ./internal/bootstrap -run '^TestSeededTemplatesRender$' -count=1 -v
go test ./internal/bootstrap -run '^TestSeededTemplatesRender$' -count=1 -v
~~~

第二条 overlay 仅将 notification_seed.go 中 notification.test 的 subject 改为未提供变量；工作区正式源码不变：

~~~diff
- {Code: "notification.test", Subject: "通道测试", Body: "通知通道测试"},
+ {Code: "notification.test", Subject: "{{.MissingAccountMailVariable}}", Body: "通知通道测试"},
~~~

~~~text
--- PASS: TestSeededTemplatesRender
exit=0

Error: template rendering failed
Messages: built-in notification.test must use provided variables only
--- FAIL: TestSeededTemplatesRender/notification.test
exit=1 (expected)

--- PASS: TestSeededTemplatesRender
exit=0
~~~

其他行为检查：

~~~powershell
go test ./internal/kernel/config ./internal/modules/identity ./internal/kernel/setting ./internal/kernel/notifier/channel -run 'Test(PublicURL|AccountLink|SupportEmail|Template.*(Condition|Support|Missing))' -count=1 -v
go test ./internal/bootstrap -run '^(TestSeededTemplatesRender|TestAccountMailVariablesAlwaysPresent|TestAccountMailAbsoluteLinkOpenAPI|TestIdentityAccountMailIntegrationVariablesAndPrivacy|TestSeedAccountTemplatesIntegrationRefreshBuiltinsAndPreserveCustom)$' -count=1 -v
go test ./internal/kernel/db -run '^TestMigrateIntegrationReversibleAndRepeatable$' -count=1 -v
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-test-groups.ps1
~~~

迁移验证在隔离测试数据库执行完整 up、重复 up、down 到零再 up；本卡没有新增/修改迁移，未回滚开发库。[迁移输出](evidence/BE-IDN-016/migrations.txt)

~~~text
--- PASS: TestMigrateIntegrationReversibleAndRepeatable (4.17s)
Source 591 = shards 590 + excluded 1
PASS: every integration test has exactly one shard or an explicit exclusion.
~~~

## 4. DoD 自检

- [x] lint：verify 中 golangci-lint run 退出 0，后续步骤执行到 all checks passed。
- [x] 全量测试：全包及集成通过，见上方输出与完整 verify 文件。
- [x] 迁移 up/down/up：TestMigrateIntegrationReversibleAndRepeatable 通过。
- [x] 无新增接口；原有邀请/重置接口补齐文档，响应结构保持原样。
- [x] 无 TODO/空实现：rg -n 'TODO|not implemented' internal 无命中。
- [x] 隐私：随机 token 仅在内存核对，真实与集成测试检查服务日志、审计、投递元数据及加密 outbox；正文截图打码。
- [x] 体积：通过 go/parser 对 git diff 和未跟踪 Go 文件做 AST 行数检查，35 个文件，最大文件 270 行、最大函数 64 行。[输出](evidence/BE-IDN-016/size.txt)
- [x] git diff --check 通过。
- [x] 测试分片守卫通过，新增集成测试各归属一个分片。[输出](evidence/BE-IDN-016/groups.txt)
- [x] 开发库清理完成，见下节。
- [ ] CI race：本机 CGO_ENABLED=0 且无 gcc，尚未推送/创建 PR，不能声明 CI race 通过。

## 5. 开发库清理

真实验收使用开发库 commercehub_test（实际 API/worker 共用的本地库）。临时租户没有删除 API，按外键顺序在一个事务内删除本卡数据；未使用禁用审计触发器或修改 schema 的方式清理。

[实际 SQL 与影响行数](evidence/BE-IDN-016/cleanup.sql)、[清理及零残留核对](evidence/BE-IDN-016/cleanup.txt)、[audit-prune 输出](evidence/BE-IDN-016/audit-prune.txt)

~~~text
临时租户 27、用户 115、管理员角色、根部门、SMTP provider 7：各删除 1 行
sys_user_role、password_history：各删除 1 行
本卡两次验证的 notify_outbox、notify_delivery、auth_token：各删除 2 行
sys_setting tenant=27：0 行；临时注册开关随 tenant.settings 删除
原有 tenant=1/user=109 保留；仅恢复本卡重发引起的旧验证令牌撤销，更新 1 行
12 个相关表均已核对本卡记录为 0
~~~

~~~powershell
go run ./cmd/cli audit-prune --before 2026-11-18T15:56:37.4388285Z
~~~

~~~text
deleted=5
~~~

执行前已核对当时开发库中的 5 条审计全部来自本卡。原有 SMTP provider 4、原有用户与租户保持不变。内置模板的新种子内容作为本卡预期变更保留；自定义模板未改。只停止了本卡启动的临时 API，保留原有 API、worker、前端和 Redis。邮箱中保留真实已收的验收邮件作为证据；对应账号/令牌已清理，地址之后会失效。

已清理：本卡限定 SQL 事务 + audit-prune --before 2026-11-18T15:56:37.4388285Z → 临时业务数据 0 残留，审计 deleted=5。

## 6. 偏差与运行注意

- 实现无需求偏差；未修改 /auth/register/verify 的验证、一次性消费或首管理员选举规则。
- 实际邮箱曾多次 Cloudflare 1102，恢复后通过用户指定的共享入口读到正文；收件与激活均已补齐。
- 初次真实投递被仍在运行的旧 worker 取走，因旧版本不支持新模板条件而 template_render_failed。通过本分支通知管理器重试后真实 SMTP 成功。部署时应更新 API 与 worker 并执行 seed。
- 临时租户最初缺管理员角色，首次打开验证页返回 503 且事务回滚、令牌未消费；补齐本租户必需角色后同一封邮件验证成功。此为验收数据修正，无业务代码变更。
- 本期 text 邮件在共享收件箱中以纯文本呈现，验收通过复制实际邮件地址打开验证页；未增加 HTML 或修改前端。
- 本地测试不替代 CI race 结果。尚未推送或创建 PR。