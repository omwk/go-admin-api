# M4b1 Replay Implementation Plan

**Goal:** 实现本轮指令限定的 000008、成员冻结、事务批处理与 ADR-018 来源仲裁。

**Architecture:** sync 服务定义历史重放契约，bootstrap 绑定当前权限、MVCC 冻结和事务执行；复用资源 Handler 与 outbox。catalog 在任何目标写入前验证来源证据。

**Tech Stack:** Go、GORM、PostgreSQL、现有 Redis 互斥与 outbox；不新增依赖。

**Spec:** docs/milestones/M4.md、docs/ADR.md ADR-018、本轮指令。

## Global Constraints

- ≤500000 成员，≤200/批；DB 事务预算 3 秒。
- 完整 RawRef 为 (fetched_at,id)；processed 等于六种处置计数之和。
- 不修改 sync_cursor；本轮不实现公平调度、pin 与全域代次 fence。
- 新增隔离原因先加入 000008 数据库白名单；不编辑已合并迁移。
- 不更改既有 ADR / PRD / 数据模型结论。

## Tasks

- [x] 000008_replay_members.up/down.sql：成员主键 (run_id,fetched_at,id)、摘要/处置、run 冻结字段/检查点/调度代次；同步 db 迁移全量期望与新增回滚断言。
- [x] ingestion.UpsertStats 补明确跳过原因；catalog/listing_source.go 在父和 SKU 写入前校验元数据和 raw_dedup；sync_page.go 将仲裁隔离持久化。测试缺元数据、非法时间、order/id 矛盾、目录多义、旧来源及同 raw 新 checksum。
- [x] sync/replay_types.go 和 replay_http.go：POST /replays、GET /replays/:id；创建冻结成功返回 201，支持已有幂等协议与权限 sync:replay:run。
- [x] bootstrap/replay_create.go：REPEATABLE READ 中校验 scope、映射与当前店铺授权，INSERT SELECT DISTINCT 成员（有界 500001）、精确 total/上下界；空集合成功、超限和归档回滚。
- [x] bootstrap/replay_batch.go：锁 job/run，每批重验创建者授权；按检查点读取 200 个冻结成员，缺 raw 整批失败；目标、快照、重算、隔离/死信、成员处置、stats 和 checkpoint 同事务。
- [x] bootstrap/replay_runtime.go：每次 outbox dispatch 仅提交一批，事务内产生下一次事件；与 sync 共用 Redis 锁和 job 行锁；故障重投依靠持久检查点恢复。
- [x] 集成测试：500 条三批、运行时新增 raw、重建执行器恢复、stats 更新失败整批回滚、上限/归档/空范围、缺 raw、权限撤销、映射冻结与来源仲裁。
- [x] 更新 M4 本轮范围/接口与验收证据，运行 powershell -File scripts/verify.ps1 -Integration -Migration，检查 TODO/敏感日志/文件函数长度。

## Verification

先针对行为写失败测试并运行，再实现；逐组件运行 go test。最终以指定 verify.ps1 的 lint、单测、真实 DB 集成及 up/down/up 全通过作为交付依据。500000 边界压测记录实际耗时，预算不通过时保持回滚并报告。


## 验收证据（2026-11-05）

最终命令：`powershell -File scripts/verify.ps1 -Integration -Migration`（测试进程设置 `GIN_MODE=release`，仅关闭路由调试输出）。退出码 0；脚本依次执行 `go vet ./...`、`golangci-lint run`、`go test ./...`、不使用缓存的数据库集成测试及 CLI 迁移。

```text
== integration ==
ok github.com/omwk/go-admin-api/internal/kernel/db 4.212s
ok github.com/omwk/go-admin-api/internal/bootstrap 224.567s
ok github.com/omwk/go-admin-api/cmd/cli 8.840s
== migrate up/down/up ==
version=8 dirty=false
version=7 dirty=false
version=8 dirty=false
all checks passed
```

- 成员冻结：500 条冻结后新增 raw 不追加入旧 run；重复 ID 只物化一次；时间范围含头不含尾。
- 故障恢复：子进程提交首批 200 条后被强制终止；重建执行器继续 200+100 条，500 个成员恰好处置一次，库存快照恰好 500 条，sync_cursor 保持为空。
- 整批回滚：第二批目标写入后由数据库 trigger 注入 stats 更新失败；listing、SKU、库存与 checkpoint 均保留首批状态；移除故障后继续完成。
- 来源仲裁：旧 raw 的父与全部 SKU 不变且 skipped_stale+1；同 raw 新 checksum 更新；缺保留区、时间非法、order/id 矛盾及目录多义均零目标修改并隔离。
- 范围限制：500001 成员返回 60601，run 与成员均不残留；归档范围返回 60601；空集合立即 success、无派发事件。
- 容量：50 万成员最大在线集合专项测试冻结提交 1.936072s，小于既有 3 秒预算；完整脚本再次覆盖该测试。新增 raw 目录索引支持身份核验；冻结事务禁用 nested loop，避免新批量数据尚未 auto-analyze 时的二次方扫描；未修改全局预算或配置。
- 幂等与权限：201 幂等响应一致；当前店铺范围、请求人撤权及授权代次重验通过。映射 version/checksum/spec/函数集版本冻结，不随 active mapping 切换。
- 质量：`git diff --check` 无错误；`rg -n 'TODO|not implemented' internal/` 无匹配。逐项核对 replay 日志，仅记录动作、run_id、raw_id，不记录 raw、凭据或个人信息。
- 长度检查：`go run ./bin/m0-verify/_tools/sizecheck.go` 输出 files=343、max_file_lines=357；唯一超限是 main 已有的 `internal/modules/identity/release.go:Apply`（93 行），本轮未改动。全部新增/修改函数 ≤80 行，文件 ≤400 行。

## 偏差与后续

- 按本轮指令，公平调度、在线 pin、持久 grant 与调度代次 fence 留第二轮；本轮预留 scheduling_generation。
- 全仓函数长度检查保留上述既有超限；本轮验收脚本全部通过。本机 CGO_ENABLED=0，race 仍须由 CI 验证。
- 本机早期 000008 试跑状态曾缺少后续新增索引；已在校验九列齐全、成员表为空且版本恰为失败回滚状态后，事务内补齐索引并恢复匹配版本，再完成实际 8→7→8 验收。临时诊断工具已清理。
