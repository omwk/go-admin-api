# M9c · 账单、容量与项目收尾验收

分支 `feat/M9c-statement-capacity`，基线 `cffa3f3`；2026-11-10。按 ADR-023 和本批修订 A：PG 持久裁决、合并往返、5 秒版本状态缓存。无新依赖，仅新增迁移 21/22。交付范围为账单、基本退款/补扣、管理权限审计、保留保护、容量校准与收尾；本报告区分本批完成和原 PRD 的历史缺口。

## REQ-BILL-001～008 逐项对照

| 需求 | 实现与证据 | 验收结论/实际边界 |
|---|---|---|
| 001 钱包、冻结、币种、并发 | `TestBillingGatewayIntegrationConcurrentAdmission`：100分/5分/30并发恰好20成功、10不足；`TestBillingIntegrationConditionalConcurrentFunds`、`ConcurrentSameCall`；wallet/ledger 非负及版本快照不变量 | 已覆盖；B 为账面余额，available=B−F，PG 条件更新，非 Redis 裁决 |
| 002 定价、套餐、免费 | `TestBillingEntitlementIntegrationPriorityAndSingleSource`、`ExpiryOrderAndFragments`、`PurchaseSnapshotAndRollback`；本批 draft 将套餐购买与 API/权益用量分列 | ADR-023 的固定线性单价、免费→最早到期付费→钱包、单调用单来源已覆盖；无自动续费/阶梯/FX |
| 003 预扣、结算、释放、幂等 | `TestBillingGatewayIntegrationAtomicMeterAndLostCommit`、`FailureAndCrashRecovery`、`RecoveryRacesLateFinish`、`TestBillingHTTPIntegrationSignedReplayAndReceipt`；`AdmissionRoundTrips` 现金/两种权益均为 1 次准入业务 SQL | 已覆盖；服务端 call_id/签名 v2 替代旧 trace_id 资金身份，持久事实与终态原子性保留 |
| 004 用量日聚合 | `TestBillingReconcileIntegrationCleanAndOneCentAlert`、`DualUTCDates`、`SourceAttribution`；源调用/日志/meter/流水精确核对 | **部分覆盖原 PRD**：usage_daily 实际粒度为 app/currency/服务日，没有 route_id 维度；按路由日报留待后续裁决，本批不改 M9b 模型 |
| 005 月账单与调用明细 | 本批 `TestBillingStatementIntegrationDraftExactRebuildAndScope`、`IssueImmutableAndLateCall`、`RefundBoundAndNextPeriod`、`CrossMonthSupplementPreservesIssued`；16步真实冒烟 | 已覆盖：精确分、明细下钻、draft可重建、issued拒绝更新/删除、跨月后续调整 |
| 006 人工充值/兑换码/不可变流水 | `TestBillingHTTPIntegrationRechargeManagementAndRedeem`、`TestBillingIntegrationCodeExpiryTenantAndConcurrentUse`、`TestBillingMigrationIntegrationAppendOnlyAndNonnegative` | 已覆盖；生成明文仅一次交付，流水 UPDATE/DELETE/TRUNCATE 拒绝 |
| 007 余额预警/欠费 | `TestBillingGatewayIntegrationConcurrentAdmission`、`UnavailableChargePort`；`SupplementOpenRetryAndEvidence` 不足时 open、充值后仅补扣一次 | **部分覆盖原 PRD**：余额不足拒绝及fail closed已覆盖；低余额阈值配置/通知尚未实现。实际不足为402/90102，90103用于钱包不可用，按M9a已落地合同；不宣称原PRD的阈值通知已验收 |
| 008 日终对账/差异告警 | `TestBillingReconcileIntegrationUnitsOrphansAndOffsettingCents`、`OneUnitLedgerDifference`、`PendingAlertRetryAndCommandResume`、`RepeatedObservationCredentials`；本批调整处置保存原观测/凭据/人/时点 | 已覆盖任意1分/1笔/1单位差异及可重试告警；补偿/人工调整不可静默改原流水 |

## 本批验收点

| 要求 | 可执行证据 |
|---|---|
| draft 金额=明细合计，单位/购买不重收费 | `TestBillingStatementIntegrationDraftExactRebuildAndScope`，真实 S3 第16步：总额5分=唯一原调用明细5分，充值100分不计消费 |
| issued 不可改，发行有门禁 | `IssueImmutableAndLateCall` 覆盖当月发行拒绝、未决调用阻断、服务层重建拒绝、DB头/明细改删拒绝；HTTP覆盖版本409与发行审计 |
| 跨月迟到/退款/补扣进入后续期 | `IssueImmutableAndLateCall`、`RefundBoundAndNextPeriod`、`CrossMonthSupplementPreservesIssued`；原issued头/明细不变 |
| 退款上限和并发 | `RefundBoundAndNextPeriod` 超原额拒绝；`ConcurrentPartialRefunds` 原收5分，10并发各退1分恰好5笔成功 |
| 补扣不足 open，处置带凭据 | `SupplementOpenRetryAndEvidence`：无凭据拒绝；不足无流水、差异仍open；补足后重试仅扣一次，resolved及唯一处置凭据包含证据引用 |
| 管理权限、归属、幂等和审计 | `TestBillingStatementHTTPIntegrationManagementIdempotencyAudit`、`JWTAndPermission`；普通用户403、无JWT401、同键异参409、生成/发行/调整审计各一次；服务owner越权拒绝 |
| 三年和未决保护 | `RetentionThreeYearsAndOpenProtection`：三年前一微秒不可归档，满三年候选；open差异/补扣保护到十年；`IssuedRetentionAndDraftProtection`：draft长期受保护，issued从发行起保留三年，候选不授权删除 |
| PG持久准入与状态缓存 | `TestBillingGatewayIntegrationAdmissionRoundTrips`；`TestGatewayStateIntegrationCacheVersionsExpiryAndFailure`、`OtherInstanceRevocation`；旧并发/回滚/恢复/配额/未知提交守卫全量复验 |
| 优化前后/并发1/50QPS | [容量报告](M9c-performance.md)及before/after JSON；现金/权益本机样本达到85/100/150ms暂定目标，无Redis配额裁决 |
| 全链真实冒烟16步 | `powershell -NoProfile -ExecutionPolicy Bypass -File scripts/smoke.ps1 -RedisAddr 127.0.0.1:16379`：16 PASS，49.38s、exit=0，schema/Redis/自有进程清理 |

## 验证命令与证据

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1 -Integration -Migration
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-test-groups.ps1
go run "$env:TEMP\codex-m9b-openapi.go"
go run "$env:TEMP\codex-m9b-size.go"
```

完整 verify 退出0，实际输出：

```text
== go vet ==
== golangci-lint ==
== go test ==
ok github.com/omwk/go-admin-api/internal/bootstrap 244.045s
ok github.com/omwk/go-admin-api/internal/modules/billing 14.088s
ok github.com/omwk/go-admin-api/internal/modules/gateway 8.638s
== integration ==
ok github.com/omwk/go-admin-api/internal/kernel/db 6.317s
ok github.com/omwk/go-admin-api/internal/bootstrap 251.154s
ok github.com/omwk/go-admin-api/cmd/cli 11.408s
== migrate up/down/up ==
version=22 dirty=false
version=21 dirty=false
version=22 dirty=false
all checks passed
```

- [x] lint/test：verify内为Makefile等价的`golangci-lint run`、`go test ./...`，本机无make；普通lint和`golangci-lint run --build-tags gatewaybench`均退出0。容量tag另验证未显式开启时安全跳过，不把SKIP说成容量测量成功。
- [x] 迁移：`go run ./cmd/cli migrate up`、`down`、`up`为22→21→22；隔离集成库同时覆盖全链回滚到底再up、并发启动和结构不变量。新增迁移21/22，既有迁移未改。
- [x] 新接口已记入M9任务卡§15及独立OpenAPI；18个操作、本地引用全部解析。
- [x] 分片：`Source 365 = shards 364 + excluded 1`，Billing46、Gateway27个集成测试各唯一归组，没有新增排除。
- [x] 无空实现：`rg -n 'TODO|not implemented' internal`无匹配；改动Go文件的Background/Print/logger扫描无新增运行日志或错误上下文替换。
- [x] 敏感信息：审查资金代码与审计字段、签名辅助客户端和冒烟输出；没有新增记录原请求、secret、兑换码、DSN或个人信息的日志。凭据只记录外部案件引用，SQL日志仍参数化/静默，公开错误安全映射。
- [x] 尺寸：`go run "$env:TEMP\codex-m9b-size.go"`检查32个改动Go文件和93个新增/修改函数，均≤400/80行；只使用标准库辅助程序，不新增仓库依赖。
- [x] 格式/脚本：`gofmt -l`、`git diff --check`无违规；三个修改/新增PowerShell入口通过parser和ASCII检查；go.mod/go.sum未改，缓存均在系统临时目录。

## 偏差与全部已知遗留

- 已批准性能范围修订：以PG批量准入/CTE和状态缓存替换“配额移Redis”，保留所有事实写入。对账批量化为次要项，本批保持既有有界分页。
- 原PRD缺口：REQ-BILL-004的路由维度日报、REQ-BILL-007的低余额阈值通知没有完成；资金安全、对账告警不能替代这两项。它们进入收尾复盘，不在M9c顺手改模型/通知架构。
- 财务归档物理清理、三年数据规模的分区/WAL/备份容量、已确认扣款的RPO及灾备验证交M10裁决；本批只提供候选检查和未决保护，账本仍不可删。
- 容量是单机短样本，缺少生产Linux、多实例/多钱包、30分钟稳定段、72小时长跑、对账并跑及PG锁/池/WAL细项。已达到的数值不作为生产SLA或最大持续QPS。
- 发行入口由管理人员触发；没有月度自动发行、退款审批流、套餐剩余额度自动退费、自动续费、在线支付、阶梯或FX；后六项属于已批准边界，非伪造已完成的功能。
- 本机CGO_ENABLED=0且无gcc，race须后续CI验证；本地测试不替代CI并发安全证据。未推送/建PR/合并/打tag。
- 继承历史遗留：[backlog](../proposals/backlog.md) 的golangci-lint v2迁移、identity既有93行函数、worker HTTP就绪端点、API readyz运维鉴权、分享撤销404/403文档对齐；其#2状态快照已解决。M4a的EXPLAIN/服务端慢查询现场证据、单条resync/内部类目写接口，以及M8的任意HTTP代理/SSRF/熔断、日志检索、公开routes、逐路由策略、字节观测仍按原验收记录，不由本批替代。

完成本批报告后停止；项目进入收尾复盘，不自动开启M10或追加优化B。
