# D1 · 重放 ETA 的固定 2 秒租约超时

- 类型：缺陷修复（产品代码）
- 优先级：P1（不影响数据正确性，但影响可观测性与 CI 稳定性）
- 依赖：S2 已合并
- 预计：**0.5–1 人日**（定位为主）
- 分支：`fix/d1-replay-eta-lease`

---

## 1. 现象

重放进度/ETA 场景在负载较高的环境下偶发超时。

| 基线 | 通过率 |
|---|---|
| `8394c68`（S2 之前） | 19/20 |
| `007ce93`（S2 之后） | 17/20 |

样本 20，两组差异在统计上不显著——**不是 S2 引入的**，是既有问题。

## 2. 已知线索

报告称「Replay ETA 的固定 2 秒租约」。需要确认具体指哪一处：

- 是 Redis 锁的租约（`kernel/cache` 的 Lock）？
- 还是 ETA 计算内部某个 `context.WithTimeout(…, 2*time.Second)`？
- 还是 outbox/租约类字段？

**先定位再改**。本卡不预设结论。

## 3. 任务

| # | 任务 |
|---|---|
| D1.1 | 复现：把该场景跑 50 次，记录失败率与失败时的耗时分布 |
| D1.2 | 定位：确认 2 秒是租约、超时还是轮询间隔；确认失败时是「超时返回」还是「租约过期导致 CAS 失败」 |
| D1.3 | 判定根因属于哪一类：① 固定值过短；② 无续租机制；③ 锁粒度不当；④ 争用导致排队 |
| D1.4 | 按根因修复（见下） |
| D1.5 | 补一个能在慢环境下稳定复现的用例（用可控时钟或注入延迟，不靠真实负载） |

## 4. 修复方向（按根因选，不要在报告里混用）

| 根因 | 修复 |
|---|---|
| ① 固定值过短 | 改为可配置（沿用 S1 的配置模式），默认值按实测 P99 × 2 设定 |
| ② 无续租 | 加续租：持有期间按 1/3 租约周期续租，续租失败立即停止写入 |
| ③ 锁粒度过粗 | 缩小到单 run / 单批，避免与同步争用 |
| ④ 争用排队 | 改为非阻塞获取 + 明确返回「忙」，由调用方退避，而不是阻塞到超时 |

## 5. 硬约束

- **不得**通过放宽断言、加 sleep、加 retry 让测试变绿
- **不得**把超时值调大到掩盖问题（若确实要调大，必须给出 P99 实测依据）
- 不得改变 replay 的语义（批大小、检查点、fence）
- 修复必须同时解释「为什么生产环境会更好」，不能只是「测试变绿了」

## 6. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 复现率 | 修复前贴 50 次失败率 |
| 2 | 修复后 | 同场景 50 次零失败 |
| 3 | 根因说明 | 明确指出属于 ①–④ 哪一类，以及证据 |
| 4 | 慢环境用例 | 新增用例能在无负载情况下稳定复现原问题（否则视为未定位） |
| 5 | 生产语义 | 说明默认参数取值依据（P99 实测或配置项） |
| 6 | 全量 | `scripts/verify.ps1 -Integration -Migration` 通过 |
| 7 | 不回归 | 业务行为测试数量不变 |

## 7. 可粘贴的 Codex prompt

```
修复 docs/tasks/D1-replay-eta-lease.md。先定位再改，不要猜。

必读：本卡、internal/bootstrap/replay_*（ETA/进度相关）、
internal/kernel/cache（Lock/租约）、internal/modules/sync/replay_*

1. 先复现：把出错场景跑 50 次，记录失败率与失败时的耗时
2. 定位「固定 2 秒」到底在哪：Redis 租约？context 超时？轮询间隔？
3. 判定根因属于 ①固定值过短 ②无续租 ③锁粒度过粗 ④争用排队
4. 按根因修复；若需要新配置项，沿用 S1 的模式（默认值不变或按 P99 给依据）
5. 补一个不依赖真实负载也能复现的用例（可控时钟/注入延迟）

禁止：放宽断言、加 sleep、加 retry、无依据地调大超时。

验收：
- 修复前 50 次失败率（贴数字）
- 修复后 50 次零失败（贴汇总）
- 根因明确到 ①–④ 之一，附证据
- 新用例在空载下能稳定复现原问题
- verify -Integration -Migration 通过

完成后停下报告，不要开 M5a。
```

## 8. 为什么值得插在 M5a 之前

1. **它是一个正在发生的产品缺陷**：ETA 算错会让运维误判重放进度
2. **它污染 CI 信号**：5% 左右的假红，正好落在「会怀疑但不确定」的区间——这种噪声最消耗信任
3. **成本低**：定位加修复 0.5–1 天，而 M5a 是 8–13 天

## 9. D1 执行记录（2026-11-07）

### 定位与验证计划

- [x] 在 `e9edcc8` 原产品代码上执行 ETA 场景 50 次。
- [x] 增加单批耗时观测；自然负载未重现，改用注入延迟区分 context 截止、Redis 过期与 DB fence。
- [x] 用固定业务时钟与注入延迟验证单一根因。
- [x] 按①配置化修复；保留批大小、检查点、fence 和原有行为断言。
- [x] 同场景执行修复后 50 次，并执行 `scripts/verify.ps1 -Integration -Migration`。

### 修复前空载基线

```text
go test -json -count=50 -timeout 30m ./internal/bootstrap -run '^TestReplayIntegrationETAUsesWallClockAndExpires$'
50 passed, 0 failed (0%); package PASS 158.926s
```

本轮未复现，没有失败样本可报告失败耗时分布。单次总耗时 2.99–4.57s，包含夹具与三批执行，不用它推算单批租约的 P99。

### 修复前负载采样与受控复现

在只增加单批 `t.Logf`、产品实现仍为 `e9edcc8` 时，同时启动三个相同的 50 次 ETA 测试进程。每轮处理三批、每批 200 条，未增加 retry 或等待：

```powershell
go test -json -count=50 -timeout 30m ./internal/bootstrap -run '^TestReplayIntegrationETAUsesWallClockAndExpires$' > $env:TEMP/d1-replay-before-load-0.jsonl
# 另两个并发进程分别写 before-load-1.jsonl、before-load-2.jsonl。
```

| 进程 | 失败 / 场景数 | 单批样本数 | 单批 P99 | 单批最大耗时 | 包耗时 |
|---|---|---|---|---|---|
| 0 | 0/50（0%） | 150 | 0.9659591s | 1.0109223s | 174.152s |
| 1 | 0/50（0%） | 150 | 0.9730488s | 0.9903579s | 174.192s |
| 2 | 0/50（0%） | 150 | 0.9568052s | 1.0059827s | 174.109s |

含空载共 200 次自然运行，未获得自然失败样本；不能据此计算失败分布，也不能声称重现了历史 19/20、17/20 的现场。保留该统计限制，用以下受控实验验证机制。

新增 `TestReplayIntegrationDeadlineRollsBackWrittenTargets/configured_context_budget`：固定 `store.now`，在真实 Upsert 之后注入一次 2100ms 的操作延迟，设置 `CH_SYNC_REPLAY_BATCH_TIMEOUT=5s`，要求目标写入、计数、检查点和完成状态一起提交。旧实现不读取该配置，仍在 2s 截止，因此这个成功断言稳定失败。

```powershell
go test -json -count=50 -timeout 10m ./internal/bootstrap -run 'TestReplayIntegrationDeadlineRollsBackWrittenTargets/configured_context_budget' > $env:TEMP/d1-replay-controlled-red-50.jsonl
```

```text
旧产品实现：0 passed, 50 failed（失败率 100%）；package FAIL 203.288s
50 次均为 context deadline exceeded
失败单批：min=2.1482426s, median=2.1524109s, P95=2.1648403s,
          P99=max=2.2108755s
```

P99 使用 nearest rank（排序后第 ceil(0.99*N) 个）；50 个样本的 P99 即最大值。计时从 `ProcessReplay` 调用前开始，到错误/成功返回结束。初步 10 次诊断同时确认目标行、processed、checkpoint、finished_at 均未提交。

### 定位结论：① 固定值过短

| 位置 | 原来的 2s 含义 | 证据 |
|---|---|---|
| `sync_grant.go/acquireGrant` | replay 调度 grant 的持有预算，保存为 `sync_job.options._scheduler.deadline` | replay class 原硬编码 `2*time.Second`；普通 sync 为 60s |
| `replay_batch.go/processReplayWithGuard` | 从 grant 剩余时间派生真实 `context.WithTimeout` | ETA 测试调用 `ProcessReplay`，传入的 Redis guard 为 nil；没有 Redis 也会稳定失败 |
| `sync_runtime.go/syncGuard` | worker 的 Redis 互斥租约 TTL | replay 原来也是 2s；普通 sync 为 30s 并续租；replay 不延长其调度 slice |
| `replay_admin.go/replayProgress` | ETA 读取与计算 | 无 2s 预算或 2s 轮询 |

固定业务时钟后，DB grant 的逻辑时间不会过期，但注入慢操作仍得到 `context.DeadlineExceeded`，说明受控失败来自批次真实 context，而非租约过期导致 CAS/fence 失败。原有推进业务时钟 3s 的测试则继续得到 `errGrantFence`，两种路径可区分。

本缺陷归为①：合法批次耗时超过硬编码的调度预算。②不解释无 Redis 的直接调用，且自动续租会延长既定让渡 slice；③与④也不解释没有竞争者的受控失败。Redis 获取本来就是非阻塞 SET NX，忙时明确返回冲突；本次不修改锁粒度、队列或续租策略。历史固定值来自 M4b1 提交 `8db2155`，在 `8394c68` 与 `007ce93` 均存在，符合既有缺陷的判断。

### 实现与生产参数

新增 `sync.replay_batch_timeout` / `CH_SYNC_REPLAY_BATCH_TIMEOUT`（Go duration），沿用 S1 的 defaults → YAML → env 优先级。非法、空、非正或溢出值回退默认值，合并为一条仅含配置键名的告警。直接构造的零值配置也回退默认值。

- 生产默认仍为 2s：三个进程中最大的单批 P99 为 0.9730488s；乘 2 得 1.9460976s，向上取整秒为 2s。样本是本机 200 条标准批次，不代表所有生产环境。
- 集成环境覆盖为 5s：受控慢批次 P99 为 2.2108755s；乘 2 得 4.421751s，向上取整秒为 5s。这是注入延迟的测量，不冒充真实生产负载 P99。修复后增加 `make test` 背景负载的原 ETA 复测又测到单批 P99 2.399212s，乘 2 为 4.798424s，仍向上取整为 5s。
- `testenv.Timeouts` 仅在有 `TEST_DATABASE_URL` 时提供 5s；显式 env 优先，`verify -Integration` 自动注入并恢复原值。
- API/worker 构造、replay dispatch、普通 sync 中的 replay class 都将同一配置传到 grant 和 Redis guard；真实 context 从 grant 截止时间派生。调整数值不会意外启用 replay 续租。
- 慢生产环境可按当地**单批** P99×2 覆盖该配置，使超过 2s 但在已标定预算内的合法批次可以完成。其他父 context 和 S1 数据库事务预算仍是上限，应一起核对。未配置的部署仍保持 2s。
- 默认让渡约束仍为 2s+5s=7s；主动覆盖后对应上限为配置预算+5s。批大小 200、检查点、epoch、双重 DB fence、批次原子性不变。

回归扩展放在现有测试的子测试中：`real_context_deadline` 验证默认超时整批回滚，`configured_context_budget` 验证慢批次成功与持久化 5s deadline，`configured_fence` 验证覆盖后的截止时间仍阻止提交；Redis 子测试用可控时钟验证 4s 时拒绝抢锁、5s 时可接管。原有过期/旧 epoch/7s 让渡断言未删除或放宽。2100ms timer 只用于注入慢操作，不在生产路径增加 sleep、retry 或续租。

### 验收记录

修复后对同一受控场景运行相同的 50 次命令：

```powershell
go test -json -count=50 -timeout 10m ./internal/bootstrap -run 'TestReplayIntegrationDeadlineRollsBackWrittenTargets/configured_context_budget' > $env:TEMP/d1-replay-controlled-green-50.jsonl
```

```text
50 passed, 0 failed（0%）；package PASS 202.132s
成功单批：min=2.1562951s, median=2.1605009s, P95=2.1712791s,
          P99=max=2.1831888s
```

受控测试在无外部负载时完成。前后均只有一次 2100ms 注入；旧实现固定 2s，修复后通过正常配置路径使用 5s，目标/计数/检查点/完成状态均有实际提交断言。不是通过重复调用业务操作、缩短延迟或放宽断言得到成功。

静态核对：bootstrap 源码测试函数名称集合 186→186（包含 authzbench 文件，不计 TestMain；普通构建注册 185 个），集成测试名称 148→148，added=[]、removed=[]。分片守卫仍为 `Source 149 = shards 148 + excluded 1`。新回归为现有顶层测试内的子测试。

原 ETA 场景同样启动三个并发进程，每进程 50 次，使用集成环境 5s 覆盖：

```powershell
go test -json -count=50 -timeout 30m ./internal/bootstrap -run '^TestReplayIntegrationETAUsesWallClockAndExpires$' > $env:TEMP/d1-replay-after-load-0.jsonl
# 另两个并发进程分别写 after-load-1.jsonl、after-load-2.jsonl。
```

| 进程 | 失败 / 场景数 | 单批样本数 | 单批 P99 | 单批最大耗时 | 包耗时 |
|---|---|---|---|---|---|
| 0 | 0/50（0%） | 150 | 2.1777213s | 2.3983162s | 216.605s |
| 1 | 0/50（0%） | 150 | 2.3611547s | 2.3646461s | 218.866s |
| 2 | 0/50（0%） | 150 | 2.399212s | 2.4285575s | 217.520s |

合计 150/150 通过。此轮同时运行了 `make lint`、`make test`，负载高于修复前的三个 ETA 进程，不能把两轮耗时差异当作配置修改引起的性能变化，也不把自然场景 0→0 作为因果证明。因果证据来自相同延迟的受控用例 50 次全失败→50 次全成功；实际负载下超过 2s 的完整提交则补充说明 5s 覆盖的作用。

```text
make lint
golangci-lint run
exit=0

make test
go test ./...
ok github.com/omwk/go-admin-api/internal/bootstrap 146.816s
ok github.com/omwk/go-admin-api/internal/worker 7.920s
exit=0
```

最终代码的完整验证：

```text
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1 -Integration -Migration
== go vet ==
== golangci-lint ==
== go test ==
== integration ==
budgets: db.txn_timeout=15s cache.op_timeout=5s db.migrate_timeout=5m sync.replay_batch_timeout=5s
ok github.com/omwk/go-admin-api/internal/kernel/db 3.336s
ok github.com/omwk/go-admin-api/internal/bootstrap 109.266s
ok github.com/omwk/go-admin-api/cmd/cli 8.809s
== migrate up/down/up ==
version=9 dirty=false
version=8 dirty=false
version=9 dirty=false
all checks passed
exit=0
```

逐条验收：

| 项 | 证据 |
|---|---|
| 修复前 50 次失败率 | 原 ETA 空载 0/50，额外三个负载进程各 0/50；历史自然失败未复现。受控慢批次 50/50 失败，100%，分布见上 |
| 修复后 50 次零失败 | 同一受控用例 50/50 通过；原 ETA 三个负载进程各 50/50 通过 |
| 根因分类与证据 | ①；固定业务时钟、无 Redis/竞争者时，2s 真实 context 对 2.1s 慢操作稳定返回 DeadlineExceeded；5s 覆盖成功 |
| 空载回归 | 同一注入和成功断言在旧产品代码 50 次全红，在配置接通后 50 次全绿；默认超时与配置过期仍整批回滚 |
| P99 依据 | 生产默认 2s 对应较轻负载 P99×2 向上取整；慢环境 5s 对应注入与更重实际负载 P99×2 向上取整；见参数说明与测量边界 |
| 全量验证 | 上述 `verify -Integration -Migration` exit=0；make lint/test exit=0 |
| 业务行为测试数量 | bootstrap 测试函数名称 186→186，集成测试 148→148；分片 Source 149 = shards 148 + excluded 1；无删除或放宽原有断言 |

补充自检：

- 无新增 HTTP 接口、表或迁移；接口表不适用。`go.mod`、`go.sum`、迁移与既有 PRD/数据模型/ADR 均未修改。
- `rg -n 'TODO|not implemented' internal/`：无匹配。
- 日志审查：检查 Go 差异中新加的日志；运行时只有配置键名告警，测试日志仅单批序号、耗时与错误，没有 DSN、凭据或业务敏感字段。
- `git diff --check`：通过。对 `git diff --name-only -- '*.go'` 列表使用 Go parser/AST 检查，改动 Go 文件最大 235 行，函数最大 63 行（限额 400/80）。
- 未新增依赖、retry、生产 sleep、续租或跳过测试。原 Redis 测试已有的 750ms 等待保持原样；新增 timer 是任务卡要求的慢操作注入。

偏差与边界：自然场景的历史失败本轮未重现；不把 0/50→0/50 宣称为失败率改善，也不把本机 P99 外推为生产保证。①的因果验证来自受控批次；生产慢环境需按当地单批 P99 配置预算。开发期间曾在 Grant 并行夹具前调用 t.Setenv，定向测试立即拒绝；已改为仅选择该 store 实例的生产默认值，未改并发调度与行为断言。最终全部验证在此修正后通过。

原始 JSONL 与完整日志保存在本机 `%TEMP%/d1-replay-*`，未写入仓库。当前改动留在本地分支，未提交、未推送；未运行 CI/race，也未开展 M5a。
