# BE-IDN-020 邮箱变更实现计划

目标：按任务卡两步验证邮箱，复用 auth_token 和加密通知 outbox，不改前端或引入依赖。

- [x] 先添加 HTTP 回归：202 两封邮件、验证前后登录、400/409、24h/单次/覆盖、隐私与直接 profile 绕过。
- [x] 新增 000036 迁移：扩展 purpose=email_change，保存新旧邮箱用于验证和定向投递；down 删除本 purpose 的记录后恢复约束。
- [x] 身份服务在用户锁下检查邮箱、撤销旧申请、存摘要并排队；匿名验证按全局唯一摘要找到租户，再在用户锁下重读令牌并原子更新邮箱、消费令牌、写掩码审计。提交后使资料缓存失效。
- [x] 新邮件确认模板 identity.email_change；旧邮箱安全提醒 identity.email_change_notice；加密 outbox 保存请求元数据，投递时重新检查令牌与用户状态，地址来自 auth_token 新旧邮箱。
- [x] 同步变量声明/示例、全模板渲染守卫、安全邮件不可退订、OpenAPI 请求和响应。
- [x] 运行聚焦集成、全量 verify、迁移 up/down/up、CI 分片守卫、格式/体积/敏感信息检查。
- [ ] 使用用户指定两个收件箱真实发信，核对邮件后从收到的链接提取令牌完成 API 验证，检查旧/新邮箱登录和 /auth/me。（两封真实收件列表已核对；正文接口 401，API 验证临时改用加密 outbox 令牌，该收件正文门槛尚未完成。）
- [x] 清理本卡验收业务数据和审计，在任务卡验收记录逐条填写 §3 十项证据，然后停下报告。

实现文件按职责：identity/account_email_change*.go、repo/account_email_change.go、bootstrap/identity_email_change*.go、既有 account_profile/link/token/notification/self_http、notification_seed、channel/variables、OpenAPI、配套回归与本卡证据。

验收命令：go test ./internal/bootstrap -run TestIdentityEmailChange -count=1 -v；powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1 -Integration；go test ./internal/kernel/db -run TestMigrateIntegrationReversibleAndRepeatable -count=1 -v；scripts/check-test-groups.ps1。
