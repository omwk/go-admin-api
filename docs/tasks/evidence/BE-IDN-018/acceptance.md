# BE-IDN-018 验收记录

分支：feat/be-idn-018-notification-complete；起点：af80e33。范围仅后端。

## 十五条验收

| # | 证据与结果 | 原始记录 |
|---|---|---|
| 1 | name=验证注册邮箱；verify/reset/invite 的关闭请求均为 409「安全邮件不可关闭」 | acceptance-tests.txt / TestAdminNotificationPreferencesIntegrationComplete |
| 2 | notification.default 可关闭，重读仍为 false；旧安全模板 false 不能阻止投递 | 同上 |
| 3 | 15 个内置 code 声明与种子、生产者变量一致；有名称、说明、示例、必填标识；无变量模板返回空列表 | acceptance-tests.txt / TestAdminNotificationTemplateDeclarationsMatchProducers、TestAdminNotificationTemplateToolsIntegrationComplete |
| 4 | 正文引用 UndeclaredForCode → 400 且 message 含变量名；主题、条件中的未知变量也有具名校验 | acceptance-tests.txt；channel/variables_test.go |
| 5 | ID 预览用声明示例，subject/body 已渲染、不含双花括号；跨租户模板 ID → 404 | acceptance-tests.txt / TestAdminNotificationTemplateToolsIntegrationComplete |
| 6 | 模板试发返回投递 ID；本地真实 TCP SMTP 接收器核对配置测试收件人、精确模板正文及重放仅一次 | 同上 |
| 7 | pending 时掩码已持久化；发送时修改目标地址后重新解析收件人；旧记录不回填 | 同上；live-send.txt 的 delivery=99 status=pending mask_present=true |
| 8 | all=4；重叠角色去重后=2；重复用户 ID 去重后=2；失效范围返回 400/404 | acceptance-tests.txt / TestAdminNotificationBroadcastAudienceIntegrationComplete |
| 9 | 开发库真实公告 2；目标用户 133 登录后收件箱 1 条，标题/正文正确；非目标用户 134 登录后 0 条；另有自动测试检查 4 种非目标用户 | live-send.txt、live-verify.txt；acceptance-tests.txt |
| 10 | 开发库真实 SMTP 投递 99：attempts=1、delivery=sent、outbox=sent，队列仅目标 1 条；实际邮箱列表有本公告及 2026/11/19 07:45:00 收件时间 | live-verify.txt、mail-receipt.jpg |
| 11 | notify:manage 单独拥有者访问三个公告接口均为 403；独立广播授权后成功；非平台管理员跨租户用户/角色 → 404；内置超管 grant=1 | acceptance-tests.txt / TestAdminNotificationBroadcastPermissionIntegrationComplete、TestAdminNotificationBroadcastAudienceIntegrationComplete |
| 12 | 公告审计恰好 1 条，含 title/channels/audience/recipients；幂等重放不重复；审计失败回滚消息、队列、历史、幂等记录 | live-verify.txt；acceptance-tests.txt / TestAdminNotificationBroadcastDeliveryIntegrationComplete、TestAdminNotificationBroadcastEmailOnlyAndRollbackIntegrationComplete |
| 13 | 列表有 title/summary/template_name；旧消息回退模板名；中文摘要恰好 120 字；模板筛选契约通过 | acceptance-tests.txt / TestAdminNotificationInboxContentIntegrationComplete |
| 14 | 新增六个 HTTP 操作、字段集合双向一致、实际 HTTP 状态/响应校验通过 | acceptance-tests.txt / TestSystemNotificationOpenAPIComplete、TestSystemOpenAPIRoutesAndPermissionsMatch |
| 15 | 全量 verify -Integration 通过；末行 all checks passed；unit bootstrap 448.339s，显式集成 db 7.764s / bootstrap 395.670s / CLI 33.912s | verify-final.txt |

## 验收命令

~~~powershell
go test -v ./internal/bootstrap -run 'Test(AdminNotification.*Complete|AdminNotificationTemplateDeclarations|SystemNotificationOpenAPIComplete|SystemOpenAPIRoutes)' -count=1
go test ./internal/kernel/notifier/channel -count=1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1 -Integration
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-test-groups.ps1
go test -v ./internal/kernel/db -run 'TestMigrat.*Integration' -count=1
~~~

专项验收 PASS，bootstrap 4.282s。集成分片：Source 602 = shards 601 + excluded 1。迁移全链、到底回滚再 up、并发初始化、结构不变量 PASS（10.570s）。没有改动迁移。

全量 verify 最终退出码 0，go vet / golangci-lint / go test ./... / 显式串行集成均通过，末行 all checks passed。早期登记/菜单实验导致的失败已修复；没有删减行为断言。最终公告 API 不增加菜单种子，具体权限的菜单投影按 FE-IDN-013 边界登记原因。

## 真实发送

2026-11-19 07:44:55（Asia/Shanghai）在开发库经公告 HTTP 接口创建；使用原有 SMTP 服务商配置的临时租户副本，由现有 worker/outbox 发送。标题：BE-IDN-018 验收公告 20261118T234455Z；正文：这是通知完整性验收的真实投递。目标用户应收到此公告，非目标用户应没有此公告。

live-send.txt 保留首次原始结果：创建、pending 掩码、两个用户认证读取成功，最后立即断言 sent 时仍是 pending。常驻 worker 已先领取任务；后续独立查询 live-verify.txt 确认 sent、仅发送一次，未重复发送。

实际邮箱显示本次公告标题及收件时间，见截图。第三方共享邮箱的详情接口返回 Failed to load message，未据此声称浏览器核对了邮件正文。正文在目标用户登录后的站内接口、开发库持久化、本地 TCP SMTP 接收测试中分别核对。

![真实邮箱收到公告](mail-receipt.jpg)

## 清理

已清理：临时 tenant=34 审计 5 条、outbox 1、delivery 1、notification 2、服务商副本 1、幂等记录 1、刷新令牌 3、用户 3、部门 1、租户 1；相关表复查为 0。未临时更改系统配置。原 SMTP 服务商仍启用，原有 8 条无关审计保留。cleanup.txt 列出 DELETE 语句、实际影响行数与清理后断言。

前一次初始化中断留下的 tenant=33 已在发送前清理。保留正式产物：notification.broadcast 内置模板及超管广播权限；实际邮箱的验收邮件作为收件证据保留。

## 自检

- Windows verify 覆盖 go vet、golangci-lint、go test ./... 和串行集成测试；对应 Makefile lint/test。
- 改动 Go 文件 AST：files=32、max_file_lines=265、max_function_lines=71，见 size.txt。
- rg -n 'TODO|not implemented' internal/ 无命中；git diff --check 无错误。
- 人工检查新增审计/错误/日志及验收输出：不含真实认证 token、密钥或明文目标地址；地址只用于加密服务商配置、加密 outbox 与发送。接口返回掩码；截图保留既有共享邮箱标识以证明收件。
- 复用原 outbox/worker；未加依赖、表、配额、定价、频率规则；未回填历史掩码。
- 本机 CGO_ENABLED=0，race 待 CI 确认；本次未创建 PR 或运行 CI。

## 偏差与兼容说明

1. 偏好保留既有按 channel 分行、enabled 为 bool 的契约，增加 name/transactional；与卡中按渠道分组的 JSON 示意形状不同，避免破坏现有客户端。实际字段已由 OpenAPI 和双向测试固定。
2. 加密配置支持 test_recipient；旧配置未填时模板试发回退 from_address，OpenAPI 已说明。预览/试发只用无认证令牌的示例变量。
3. 广播权限已 seed 授予内置超管。菜单投影对该具体权限登记明确原因，公告 UI/菜单属于 FE-IDN-013。
4. 清理采用 audit-prune 相同的事务维护标志，按本卡临时租户限定 DELETE；未执行会删除原有 8 条无关审计的全库 audit-prune --before 当前时间。
