# M9b 权益与对账实施计划

**Goal:** 实现 REQ-BILL-004/005/006 的权益优先级、单位流水、UTC 日聚合、三方对账、告警及有凭据补偿。完成后停止，不进入 M9c。

**Architecture:** 沿用 ADR-023 和 M9a 的 Start/Finish 原子事务。收费回执固定一个来源；权益通过 PG 条件更新预留和终结，失败释放、到期核销均留单位凭据。对账读取独立调用意图、日志、meter 和账本，逐调用核对并校验资金/单位控制账；观测不可覆盖，补偿只追加凭据并重新核验。

**Tech Stack:** 现有 Go 1.23、GORM、PostgreSQL、Gin、slog 和 testify；不新增依赖。

**Spec:** `docs/milestones/M9.md`、ADR-023 §3/4、`docs/proposals/M9-review.md` Q3/Q4、本次 M9b 指令。

## 约束

- 免费权益 → 最早到期的付费权益 → 显式钱包回落；选择足以覆盖整次调用的一个来源。
- 套餐购买与使用分账，无自动续费；免费路由仍只计成功 meter。
- 服务日按 called_at UTC，入账日按流水 occurred_at UTC，任意 1 分/1 笔/1 单位差异均记录并告警。
- 保留 M9a 幂等、短事务、非负余额与互斥终态；账单和容量工作属于 M9c。
- 每个 Go 文件 ≤400 行，每个函数 ≤80 行；新增集成测试使用 TestBilling 域。

## 实施任务

- [x] 1. 新增 `000020_billing_entitlement` up/down；在任务卡记录实际字段及接口，增加权益/单位/套餐/差异/运行水位/补偿凭据载体。验证迁移全链及只追加约束。
- [x] 2. 增加 billing 权益服务和 bootstrap 存储；扩展 Charge 保存来源、权益与单位快照。验证 10 次免费→付费→钱包、到期顺序、整笔覆盖、并发、失败释放、在途到期与幂等。
- [x] 3. 增加套餐配置、购买/赠送与权益/流水查询；管理写入沿用幂等、权限和 owner scope。验证购买扣款和权益授予的原子性、不重复扣费与版本快照。
- [x] 4. 增加逐调用差异规则和账本控制核验；日聚合按 `(tenant,app,currency,service_date)` 幂等重算，保存 run 截止水位。验证 1 分、缺失 charge、1 单位、孤儿、相互抵消和跨日入账。
- [x] 5. 增加每日昨日 UTC 任务、指定日期触发与补偿凭据查询；补偿不改原流水，重验一致才 resolved。验证重跑、告警与事务失败重试。
- [x] 6. 执行 `scripts/verify.ps1 -Integration -Migration`、CI 分组检查、格式/尺寸/敏感日志检查，记录实际输出并停止报告。

## 主要文件与接口

- `internal/modules/billing/entitlement*.go`：权益、套餐、单位流水 DTO 与服务；调用来源仍由 `Service.Prepare/Freeze/Settle/Release` 编排。
- `internal/bootstrap/billing_entitlement*.go`：有归属与可用额条件的预留、消耗、到期；`billing_plan.go`：套餐/授予持久化。
- `internal/modules/billing/reconcile*.go`：逐调用/控制账差异规则、服务与安全日志告警。
- `internal/bootstrap/billing_reconcile*.go`：分批事实读取、不可变观测、日聚合、水位和补偿凭据；`billing_schedule.go`：每日任务。
- `internal/modules/billing/http_*.go` 与 permission registry：已批准的套餐、权益、对账和凭据管理接口。
- `internal/bootstrap/gateway_billing.go` / `gateway_call_store.go`：收费来源与实际现金/单位投影；不改变业务执行逻辑。
- `internal/bootstrap/billing_*integration_test.go`、`internal/modules/billing/*_test.go`：上述验收回归。

## 验证命令

```powershell
go test ./internal/modules/billing ./internal/modules/gateway ./internal/bootstrap -run 'TestBilling|TestCharge' -count=1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-test-groups.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1 -Integration -Migration
git diff --check
rg -n 'TODO|not implemented' internal
```

全部实施及本地验收完成，详细证据见 `docs/milestones/M9.md` §14。未开始 M9c，未提交、推送或创建 PR；race 结果留 CI 验证。
