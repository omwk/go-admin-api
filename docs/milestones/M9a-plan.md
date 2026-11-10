# M9a 钱包账本与网关收费实施计划

**Goal:** 实现 REQ-BILL-001/002/003/007 的钱包、流水、逐调用预扣、充值和崩溃恢复；完成后停止，不进入 M9b。

**Architecture:** 沿用 ADR-023 与 M8b 两个持久事务。gateway 服务通过公开收费端口编排，bootstrap 将 billing 存储绑定到同一个 db.DB 的事务 context；事务内不做外部 IO。Start 的锁序为 call → charge → quota → wallet，Finish 为 call → charge → wallet。

**Tech Stack:** 现有 Go 1.23、GORM、PostgreSQL、Gin、testify；不新增依赖。

**Spec:** docs/milestones/M9.md、ADR-023 §1/2/7/8/10、docs/proposals/M9-review.md Q1/Q2、本次 M9a 指令。

## 约束

- 所有金额为 int64 分，固定单价 × 单位数，检查乘法/加法溢出；仅 CNY。
- balance 为账面余额，available=balance−frozen；每次资金变更与只追加流水同事务。
- call_charge 的 reserved → settled/released/expired 终态互斥，按 call_id 幂等。
- 资金并发用 PG 条件 UPDATE；管理版本变更遵循 CAS。
- 财务凭据保留三年；未决不清理。本批没有清理任务。
- 不实现权益、套餐、三方对账、日聚合、账单、容量优化。

## 执行步骤

- [x] 1. 新增 billing 金额校验测试，实现公开服务、存储端口及错误；金额正值、乘法溢出与整数边界测试通过。
- [x] 2. 新增 000019_billing_wallet up/down；包含 wallet、wallet_txn、call_charge、recharge_code 与固定价格/签名幂等字段。真实 PG 测试验证 `freeze(5), settle(3)` 得 B=97/F=0、条件更新及流水资产守恒。
- [x] 3. 接入 Start/Finish/worker，持久结果与 meter/settle 同事务。meter 失败、提交回执丢失、超时释放、终态竞争和计费端口不可用测试通过。
- [x] 4. 新增签名 v2（绑定 app_id 与 Idempotency-Key）和请求指纹，唯一映射请求键到 call_id；重放原结果/价格、处理中 409、同键异参 10006 均已验证；保留 free 的 v1。
- [x] 5. 接入管理钱包创建/查询、流水、人工充值、兑换码生成与 HMAC 兑换/余额；权限 registry、审计及独立 OpenAPI 已同步。人工充值和兑换码幂等/归属/过期/回滚测试通过。
- [x] 6. 新增 Billing CI 分片。两组独立调用服务发起 30 并发，100/5 恰好 20 成功、10 次 402/90102、B=F=0；另验证同调用 100 次并发 freeze/settle 不重扣。
- [x] 7. 任务卡实际接口与验收记录已更新；`scripts/verify.ps1 -Integration -Migration`、分片守卫、TODO/日志/长度检查及 `git diff --check` 已通过。验收输出见 M9.md §12。

## 文件责任

- `internal/modules/billing/`：金额、资金状态机、公开存储端口、管理 HTTP 与单测。
- `internal/bootstrap/billing_*.go`：同事务 PG 条件更新、兑换码/审计、集成测试；`gateway_billing.go` 仅适配公开端口。
- `internal/modules/gateway/`：收费快照、事务参与、签名幂等、回执、公开错误与 OpenAPI。
- `internal/bootstrap/gateway*.go`：新增价格字段持久化、open/admin 接线；worker 使用相同收费恢复端口。
- `migrations/000019_billing_wallet.*.sql`：只增迁移；`docs/milestones/M9.md` 记录本批实际模型和 API。
- `internal/modules/identity/permission/` 与 `scripts/check-test-groups.ps1`：新增权限定义和 Billing 集成测试归属。

## 验证断言

```go
require.EqualValues(t, 20, succeeded)
require.EqualValues(t, 10, insufficient)
require.Zero(t, wallet.BalanceCents)
require.Zero(t, wallet.FrozenCents)
require.Equal(t, wallet.BalanceCents, sumBalanceDelta)
require.Equal(t, wallet.FrozenCents, sumFrozenDelta)
```

普通 CI 只验证确定性行为，不使用耗时或统计阈值；race 仅在具备 CGO 的 CI 执行。
