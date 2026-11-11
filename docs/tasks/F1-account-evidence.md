# F1 验收证据（2026-11-11）

分支：`feat/f1-account`。功能实现、接口明细和安全收紧见 [任务卡](F1-account.md) §8。

## 定向验收

```powershell
$env:GOCACHE = Join-Path $env:TEMP 'codex-f1-go-build'
$env:GOLANGCI_LINT_CACHE = Join-Path $env:TEMP 'codex-f1-lint'
$env:CH_DB_TXN_TIMEOUT = '15s'
go test -count=1 -v ./internal/bootstrap -run '^TestIdentityAccount.*Integration'
```

该命令使用隔离 PostgreSQL 数据库、miniredis 和可控邮件发送器；最终 20 个集成测试通过（本轮同时执行 identity 单元测试）：

```text
--- PASS: TestIdentityAccountSessionsIntegrationOwnDeleteProfileAndLogoutAll (1.29s)
--- PASS: TestIdentityAccountRecoveryIntegrationUniformAcceptedAndReset (2.38s)
--- PASS: TestIdentityAccountNotificationIntegrationUnavailableAndDigestOnly (2.84s)
--- PASS: TestIdentityAccountNotificationIntegrationLockedRecoveryAndDisabledPrivacy (4.17s)
--- PASS: TestIdentityAccountAdminIntegrationForcedChangeAndReset (4.23s)
--- PASS: TestIdentityAccountExpiryIntegrationRestrictsAndRecovers (4.54s)
--- PASS: TestIdentityAccountInviteIntegrationExpiryRevokeAndBatch (6.90s)
--- PASS: TestIdentityAccountRecoveryIntegrationExpiryProductionAndAbuse (8.17s)
--- PASS: TestIdentityAccountTokenIntegrationConcurrentConsumption (8.46s)
--- PASS: TestIdentityAccountAdminIntegrationResetLinkAndAudit (9.27s)
--- PASS: TestIdentityAccountRecoveryIntegrationProductionChannelAlert (10.07s)
--- PASS: TestIdentityAccountRecoveryIntegrationAcceptedContract (10.33s)
--- PASS: TestIdentityAccountNotificationIntegrationStableRetry (11.43s)
--- PASS: TestIdentityAccountNotificationIntegrationMailLinksAndConsumedRetry (12.83s)
--- PASS: TestIdentityAccountLoginIntegrationIdentifiersShareFailures (13.51s)
--- PASS: TestIdentityAccountInviteIntegrationPresetAndSingleUse (12.91s)
--- PASS: TestIdentityAccountNotificationIntegrationKeylessPrivacy (12.09s)
--- PASS: TestIdentityAccountNotificationIntegrationInviteChannelRecovery (12.29s)
--- PASS: TestIdentityAccountOwnershipIntegrationTenantAndSessionIsolation (11.46s)
--- PASS: TestIdentityAccountPasswordIntegrationKeepCurrentAndHistory (17.21s)
PASS
ok github.com/omwk/go-admin-api/internal/bootstrap 18.783s
```

| 卡片 §5 | 证据 | 实际断言 |
|---|---|---|
| 1 邀请注册 | `InviteIntegrationPresetAndSingleUse` | 管理路由生成邀请，匿名接受，部门与角色落库，随后通过登录路由成功登录 |
| 2 邀请失效 | `InviteIntegrationPresetAndSingleUse`、`InviteIntegrationExpiryRevokeAndBatch` | 二次接受、过期、撤销均拒绝；统一「邀请或链接无效」 |
| 3 批量邀请 | `InviteIntegrationExpiryRevokeAndBatch` | 3 次成功，第 4 次拒绝；另验证 `max_uses=0` 可继续使用 |
| 4 忘记密码 | `RecoveryIntegrationUniformAcceptedAndReset`、`NotificationIntegrationMailLinksAndConsumedRetry`、`NotificationIntegrationStableRetry` | 已知/未知账号均 202；开发复制链接和 F3 邮件链接能完成重置；无通道落加密 outbox，重试保持原链接 |
| 5 重置密码 | `RecoveryIntegrationUniformAcceptedAndReset` | 令牌二次使用拒绝；旧密码拒绝，新密码登录成功；历史会话在 Redis 丢失后仍被拒绝 |
| 6 改自己密码 | `PasswordIntegrationKeepCurrentAndHistory` | 旧密码错误拒绝；当前 bearer 保留，其他 bearer/refresh 失效 |
| 7 密码策略 | `PasswordIntegrationKeepCurrentAndHistory`、`pwd.TestPasswordPolicy` | 短密码、纯字母、纯数字拒绝；最近 5 条不复用，历史仅保留 5 条，更早密码可重新使用 |
| 8 密码过期 | `ExpiryIntegrationRestrictsAndRecovers` | 时间设为 91 天前，登录标记 `password_expired`；普通认证操作 403；改密后恢复 |
| 9 登录标识 | `LoginIntegrationIdentifiersShareFailures`、`TestAccountIdentifierNormalization` | 用户名、本地手机号、E.164、大小写邮箱均可登录；混用标识累计 5 次后统一锁定 |
| 10 账号枚举 | `RecoveryIntegrationAcceptedContract`、`RecoveryIntegrationExpiryProductionAndAbuse`、`RecoveryIntegrationUniformAcceptedAndReset` | 已知/未知均 202 和 `accepted:true`；仅 dev/test 含 `dev_reset_url`、未知链接不可消费；生产无链接，IP、账号独立限流 |
| 11 我的会话 | `SessionsIntegrationOwnDeleteProfileAndLogoutAll`、`OwnershipIntegrationTenantAndSessionIsolation` | 列出多个会话、删除一个使其失效；其他用户会话 404；全部登出含当前；更新自己资料 |
| 12 全量 | `scripts/verify.ps1 -Integration -Migration` | vet、lint、全仓测试、独立集成测试通过；迁移 24 → 23 → 24，均非 dirty；最终 `all checks passed` |

测试完整函数名前缀均为 `TestIdentityAccount`，文件位于 `internal/bootstrap/identity_account_*_integration_test.go`。

额外验证：管理员建号/重置强制改密、重置后旧会话撤销、重置不解除停用、锁定账号邮件找回成功后可登录、并发邀请/重置只能一次消费、跨租户不能接受令牌。加密 outbox 解密后包含原始链接，数据库密文不含原始令牌，邮件重试保持链接和摘要不变，已消费链接不能再发送。

新契约验证：`RecoveryIntegrationProductionChannelAlert` 断言生产缺少邮件通道时仍返回 `accepted:true`、WARN 含 request_id、不含账号或令牌、租户管理员站内通知落库、指标从 1 随通道恢复投递降为 0。`AdminIntegrationResetLinkAndAudit` 断言生产管理员生成链接成功、匿名 401、无权限 403、审计区分操作者和目标且不含令牌，链接只能使用一次；加强权限断言后单独重跑：`ok github.com/omwk/go-admin-api/internal/bootstrap 2.332s`。

## 最终全量验证

```powershell
$env:GOCACHE = Join-Path $env:TEMP 'codex-f1-go-build'
$env:GOLANGCI_LINT_CACHE = Join-Path $env:TEMP 'codex-f1-lint'
.\scripts\verify.ps1 -Integration -Migration
```

进程退出码 0，关键输出：

```text
== go vet ==
== golangci-lint ==
== go test ==
ok github.com/omwk/go-admin-api/internal/bootstrap 264.494s
ok github.com/omwk/go-admin-api/internal/modules/identity 16.907s
== integration ==
ok github.com/omwk/go-admin-api/internal/kernel/db 6.841s
ok github.com/omwk/go-admin-api/internal/bootstrap 262.742s
ok github.com/omwk/go-admin-api/cmd/cli 11.824s
== migrate up/down/up ==
version=24 dirty=false
version=23 dirty=false
version=24 dirty=false
all checks passed
```

迁移由入口依次执行 `go run ./cmd/cli migrate up`、`go run ./cmd/cli migrate down`、`go run ./cmd/cli migrate up`。数据库集成测试另覆盖全链 up、down 到底再 up、并发启动和结构不变量。

## 回归维护

首轮全仓检查发现三个既有测试需适应 F1：

- 用户筛选夹具先插入再软删旧邮箱拥有者，再创建新用户，保留全部筛选、分页、范围断言，并验证软删后标识可复用。
- 订单历史迁移测试在降到旧 schema 之前完成当前版本认证；仍验证密文完整保留、重认证时间回滚失效和明文掩码约束。
- F3 迁移仍从 23 降到 22 并验证四表消失，再完整升级到自动推导的最新版本，补充四表重建和非 dirty 断言。

三处定向回归结果：`PASS`，`ok github.com/omwk/go-admin-api/internal/bootstrap 3.360s`。未删除或放宽行为断言。

## 其他检查

```text
scripts/check-test-groups.ps1:
Source 411 = shards 410 + excluded 1
PASS: every integration test has exactly one shard or an explicit exclusion.

F1 changed Go length scan:
changed Go files: 54; max file: 244; max function: 79; violations: 0
```

长度检查用 `git diff --name-only -- '*.go'` 与 `git ls-files --others --exclude-standard -- '*.go'` 获取变更文件，PowerShell 逐文件计行并按顶层 `func` / 结束花括号计函数行；无超限。`git diff --check` 无输出；`rg -n 'TODO|not implemented' internal` 无匹配。

日志/隐私检查：逐一检查新增 handler、密码/令牌服务、F3 适配与审计调用；审计只包含 actor/resource/token ID 等安全元数据、无凭证快照。F3 outbox 的密文断言验证链接和原始令牌未明文持久化；解密后与复制链接相同。投递正文仅在内存中渲染，沿用 F3 哈希化投递日志。测试敏感材料运行时生成。

本机未安装 `make`，按 Makefile 等价执行 `golangci-lint run`、`go test ./...`，统一入口为 `scripts/verify.ps1`；Go/lint 缓存位于系统临时目录。本地无 gcc，未运行 race，race 结果须由 CI 验证。

## 偏差与部署配置

- 已确认：生产匿名找回不回传重置凭证，仅 `app.env=dev/test` 额外返回 `dev_reset_url`。所有环境均含 `accepted:true`；生产人工转达使用新增的管理员 reset-link 接口，符合当前任务卡 §4.5。
- 已确认选项 A：`auth_token` 仅存 SHA-256；F3 沿用现有加密 outbox 保存原始链接，重试不改变链接或摘要。邀请邮件发送给创建者的已有邮箱供转达，管理员创建响应同时返回复制链接。
- 未配置加密密钥的本地环境仅保留 outbox 安全引用，不存明文凭证；复制链接仍可用，配置密钥后需重新签发邮件链接。
- 部署邮件前配置 `CH_APP_PUBLIC_URL` 为前端公开基址；未配置时为站内相对链接。链接中 fragment 令牌需由前端读取后提交给 API。
- 现有手机号、邮箱列和用户名部分唯一索引复用；仅新增缺少的列和两个标识索引。
