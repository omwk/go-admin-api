# M9c 账单、容量与收尾实施计划

> **For agentic workers:** Use superpowers:subagent-driven-development to implement this plan task-by-task, with task review and final review. Steps use checkbox syntax for tracking.

**Goal:** 完成 REQ-BILL-001～008 的最后一批账单、容量校准和项目收尾，并以实测和完整验收报告交付后停止。

**Architecture:** 沿用 ADR-023 的 PG 持久准入与资金事务；减少网络往返，不减少事实写入。月账单从独立财务凭据生成，发行后冻结，退款和补扣只追加至后续开放期间。复用管理权限、owner scope、幂等、审计和 S3 隔离冒烟框架。

**Tech Stack:** 现有 Go、Gin、GORM、PostgreSQL、Redis、testify、PowerShell；不新增依赖。

**Spec:** `docs/milestones/M9.md`、ADR-023 §5/6/9/10/11、`docs/proposals/M9-review.md` Q5/Q6、M9c 原始指令及后续 A 方案修订。

## Global Constraints

- 不得把配额准入改为 Redis 裁决；Redis 只能做限流桶（既有 nonce 防重放保持原协议）。
- 不得减少持久写入次数（只能减少往返）；调用意图、processing/终态日志、配额、charge、冻结/结算流水、成功 meter 仍持久化。
- 配额计数、调用意图、预扣冻结合并为单次 PG 往返（CTE 或同事务批量）；保留锁序、幂等、回滚与未知提交恢复。
- 应用/路由/密钥状态按版本缓存，有效期 5 秒；撤销走主动失效。失效故障 fail closed，跨实例不得放行已撤销请求。
- 优化前后必须用同一压测脚本对比。若合并后并发 1 open HTTP p99 仍 >100ms，停下来报告，不自行扩大优化范围或实施 B。
- UTC 自然月，int64 分，CNY；draft 可重建，issued 的金额、明细、期间不可修改。
- 调用现金消费、权益单位、套餐购买、退款/补扣分列；充值不计消费，权益使用不再计套餐售价。
- 跨月迟到、退款、补扣进入下一开放期调整，引用原交易/服务月/账单；退款累计不超过原交易；余额不足的补扣保持 open。
- 财务凭据保留 3 年，未决记录不清理；不保留请求正文或敏感凭据。
- 每个 Go 文件 ≤400 行，每个函数 ≤80 行；新增集成测试 TestBilling/TestGateway 域，恰好一个 CI 分片。
- 只新增迁移，不改旧迁移；数据模型仅追加实际载体，不修改既有结论。
- 仅本地提交；最终报告后停止，不推送、建 PR、合并、打 tag 或进入复盘实施。

### Task 1: 持久路径合并、状态缓存与同口径容量测量

**Files:** 修改 `internal/bootstrap/gateway_{quota,call_store,store,metering}.go` 及必要管理状态实现、`internal/modules/gateway/call*.go`、`scripts/gateway/benchmark.ps1`、`internal/bootstrap/gateway_benchmark*_test.go`；新增聚焦 gateway helper/测试、实施时下一号 up/down 迁移（如需 SQL 函数）；新增 `docs/milestones/M9c-performance-{before,after}.json` 与 `M9c-performance.md`。

**Interfaces:** 保持 gateway.CallStore、收费公开端口的既有调用兼容；可添加可选批量准入能力接口，通用 Store 测试替身仍走现有路径。生产接线必须使用优化路径，包含权益与现金，失败整体回滚。

- [ ] 阅读 M8c 报告、M9a/b 事务/恢复与当前 gateway/billing 路径，记录热点 SQL 次数、往返与池等待；不能把独立阶段 p99 相加。
- [ ] 先扩展同一 opt-in harness：真实 PG/Redis、付费钱包/权益实际收费接线、签名 v2、并发 1 的 direct/open/配对 p50/p95/p99、固定到达 50 QPS（真实授权领域查询）及错误/账本核验；正常 CI 不运行容量断言。
- [ ] 用未优化产品代码运行上述脚本保存 before JSON，记录样本、环境、应用/钱包分布、预热、到达调度和统计方法。不得使用 M8c 免费结果冒充 M9 基线。
- [ ] 添加确定性回归：并发额度精确、不足回滚、同幂等只记一次、未知提交回读；缓存命中/版本变化/到期、双实例主动撤销和故障拒绝。
- [ ] 实现 PG 单往返准入合并以及版本化 5 秒状态缓存和主动失效，保留所有持久事实；对账读取只在有必要时批量化。
- [ ] 聚焦测试与 lint 通过后，用完全同一脚本/参数运行 after，逐项核对资金、单位和准入计数。
- [ ] 写实测报告，对照 85ms 配对开销 /100ms open /50QPS 150ms 暂定目标；若 open p99>100ms 标记停止条件并通知主代理，不继续调参扩大范围。
- [ ] 自查尺寸、敏感日志、依赖与 diff；本地提交并写报告，注明实际通过/失败与测量限制。

### Task 2: 月账单、退款补扣与保留保护

**Files:** 新增 `internal/modules/billing/statement*.go`、`internal/bootstrap/billing_statement*.go`、`billing_adjustment*.go`、对应集成测试与下一号迁移；追加 `docs/03-data-model.md` 实际载体及 `docs/milestones/M9.md`。

**Interfaces:** 独立 `StatementService`/`StatementStore`，不扩大所有 billing.Store 替身。公开业务方法 `Generate(ctx,Actor,app,month)`, `Statements(ctx,Actor,filter)`, `Statement(ctx,Actor,id)`, `Issue(ctx,Actor,id,version,reason)`, `Adjust(ctx,Actor,input,key)`；HTTP 层依赖业务服务，不写 SQL。

- [ ] 先记录实际字段、唯一约束、状态和 API 契约；头唯一 (tenant,app,currency,period_start)，明细来源唯一，调整保存原交易、凭据、原因、actor、命令身份及 open/applied 状态。
- [ ] 编写失败验收：draft 明细求和精确；来源集/水位相同重建幂等；issued 重建/修改拒绝（服务与 DB）；跨月迟到/退款/补扣入下一期，原单不变；并发部分退款上限；不足补扣 open、重试成功一次；owner 越权拒绝。
- [ ] 从 settled call_charge、unit_txn、套餐购买 wallet_txn 和已执行调整分批生成最小财务明细，保留费率/单位/币种/服务日/入账日快照，金额溢出拒绝。发行前核对三方对账水位及未决记录，发行和审计同事务。
- [ ] 调整以 PG 原交易锁和条件更新裁决，钱包变化、只追加流水、调整凭据、差异处置和审计原子提交。不得自动退款套餐剩余单位，不静默修改历史价格。
- [ ] 实现三年保留判定和数据库/清理入口保护，测试不足三年/未决/已发行引用及超过三年且无未决的候选；不放宽既有只追加约束。
- [ ] 运行聚焦账单/调整/保留/迁移测试与 lint，自查尺寸并本地提交，写报告。

### Task 3: 管理协议、16 步冒烟与收尾文档

**Files:** billing HTTP 新文件、permission registry、bootstrap 接线、`api/openapi/billing.yaml`、管理集成测试；`scripts/smoke.ps1`、`scripts/smoke/*`、`README.md`、`progress.md`、`docs/milestones/M9c-acceptance.md`、`docs/milestones/M9.md`。

**Interfaces:** 列表/详情/生成/发行/调整/差异处置使用 JWT+RBAC+owner scope；写操作必须 Idempotency-Key、同键异参 409/10006；不引入新错误码。公开 OpenAPI 的字段与实际响应一致。

- [ ] 对照 Task 2 已记录契约编写管理 HTTP 成功/错误/权限/幂等/审计回归，注册权限并补独立 OpenAPI。
- [ ] S3 冒烟扩展为 16 步，真实执行调用→扣费→对账→生成 draft 并断言明细和金额；保留原全部断言、独占 schema/Redis 和清理核验，补更新步数回归。
- [ ] REQ-BILL-001～008 逐条给出测试/命令证据；progress 补 M9 与全部已知遗留，README 补部署、迁移、日对账、账单发行/调整、保留和容量入口。
- [ ] 运行定向管理测试、CI 分片守卫、OpenAPI 引用核验与冒烟，记录 16 步结果；本地提交并写报告。

### Task 4: 完整验收与最终报告

**Files:** 仅补验收与进度文档的实际结果；代码缺陷通过原任务 fix loop 修复。

- [ ] 执行 `powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1 -Integration -Migration`，保留完整日志与退出码。
- [ ] 检查 CI 分组、gofmt、diff、TODO/空实现、日志敏感字段、文件/函数尺寸、依赖与迁移不变量；不将缓存写入仓库。
- [ ] 汇总最终评审结果、优化前后 JSON、85/100/150ms 判定、16 步冒烟、迁移 up/down/up 和需求映射；没有证据的项保持未完成。
- [ ] 更新验收文档与 progress，报告本机 race 限制及其余已知遗留，按仓库交付模板停止。
