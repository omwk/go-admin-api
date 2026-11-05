# M4b2 第一轮执行计划

依据：`docs/milestones/M4.md`、ADR-018 §5/§8 和本轮指令。
保留 HTTP 适配器，导出使用 minio-go/v7 v7.0.83；不启用桶版本管理。

1. 新增 000009 可逆迁移，新增 export_job_row 和 job.attempt/row_total，复用 started_at。
   唯一索引 `(export_id, attempt, ordinal)` 同时供 keyset 使用。
2. catalog 定义 ObjectStore 端口和 export DTO/store；infra/storage/s3.go 实现 SDK 流式 Put/Get/Delete/PresignGet，另提供前缀扫描。
3. bootstrap persistence 创建任务与 outbox 同事务，冻结 filters/sort/fields_version/tenant/owner。
   首次 Begin 用 INSERT SELECT 一次物化完整白名单投影，事务预算 3 秒；重试复制前次物化行，保留原生成时间。
4. worker 按 ordinal 每批最多 1000 行，通过 io.Pipe 写 CSV 并计算行数/大小/SHA-256。
   上传独立 attempt 键后读回校验摘要；发布用 `WHERE id=? AND attempt=? AND status='running'` 条件更新。
   过时代次、失败与崩溃对象由引用保护清理，失败进入 outbox 重试。
5. 装配已有两个导出 API、RBAC、幂等创建和 worker dispatch；下载校验 tenant/owner，签名 TTL 为 min(1h,剩余寿命)。
   现有 outbox 默认 60 秒不足，导出事件单独使用 6 分钟 delivery lease，worker 硬预算 5 分钟。
6. 引用保护清理：保留 DB 指针；运行中的当前 attempt 不删；过时 attempt 可删；未知临时对象超过 1h 可删。每 5 分钟扫描，错误进入 outbox。
7. 测试：先验证 CSV 防注入与参数契约，再验证真实 DB 的筛选快照、10 万行、3 秒物化、强杀恢复、并发/迟到上传、清理引用保护、跨租户/owner 404。
   MinIO 集成验证预签名下载与过期 403；迁移增补全集断言并执行 9→8→9。

验收：`powershell -File scripts/verify.ps1 -Integration -Migration`；`go run ./bin/m0-verify/_tools/sizecheck.go`；`git diff --check`；扫描 TODO 与日志。
本轮不实现并发准入限额和观测阈值。

执行记录（2026-11-05）：1–7 已实现。真实 PostgreSQL + 固定版本 MinIO 的专项验收通过；全量 verify 与最终自检记录在 `docs/milestones/M4b2-acceptance.md`。

## 第二轮实施计划（2026-11-05）

目标：完成 REQ-PRD-008 的 DB 准入、24h 保留期和 ADR-018 §6 观测门禁。
约束：不新增依赖、不改迁移与既有 ADR；同一事务计数和预占 running，Redis 故障不绕过限制。

- [x] 准入：新增 `internal/bootstrap/catalog_export_admission.go`，Create/Begin 共用全局事务 advisory lock；优先检查租户 pending 10，再检查请求人 1、租户 1、全局 2。幂等重放先于准入。`catalog_export_admission_integration_test.go` 验证 10 并发、分层 429/10005、拒绝请求不落库/不派发与失败重试准入。
- [x] 保留期：在 `catalog_export_cleanup.go` 中新增到期 DB 转移，与精确键 outbox 同事务；catalog 清理入口先处理到期，再扫描；删除前重读当前引用及 attempt fence。新增到期、删除故障和引用交错集成测试，保留既有清理用例。
- [x] 观测：新增 bootstrap DB collector，注册到 `cmd/api/main.go`；暴露 running{tenant}、duration histogram、cleanup pending、failed counter，补充执行/清理年龄与失败次数用于 180/300、300/900 秒告警；worker 定时检查，仅级别变化时记录告警。使用假时钟验证阈值和 /metrics 验证跨进程 DB 数据。
- [x] 验收：运行专项测试，再运行 `powershell -File scripts/verify.ps1 -Integration -Migration`（9→8→9）；检查格式、体积、TODO、日志；在 `M4.md`、`M4b2-acceptance.md` 记录行为、证据和限制。

第二轮执行记录：全部导出专项测试 33.548s；临时真实 MinIO 环境下完整 verify 退出 0，迁移 9→8→9，最后一行 `all checks passed`。保留第一轮记录作为历史；本轮证据见验收记录的第二轮小节。
