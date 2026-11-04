# M4b1 第二轮实施计划

目标：按 ADR-018 完成持久公平调度、事务代次 fence、在线 pin 和管理接口。

约束：不增加依赖；000008 尚未合入 main；保持 8→7→8；单批最多 200、持锁最多 2 秒；不修改既有 ADR 和数据模型结论。

1. 在 `bootstrap/sync_grant.go` 实现 job.options 保留区调度状态，包含 owner、epoch、deadline、超车计数；持久 ready_at 与 outbox 同事务更新。`withSyncGrant(ctx, job, run, fn)` 和 `ProcessReplay` 共用调度行。测试连续十次 grant、重建 store、未来任务与过期 owner。
2. 在 sync page、defer 与 replay batch 事务首先锁 job 并验证上下文 grant 和 run.scheduling_generation；事务结束再次核对截止时间。旧 worker 在新 epoch 后提交必须错误，所有目标计数保持零。
3. 扩展未合并的 000008，增加 raw_partition_pin；冻结事务持有维护共享锁，退休维护持有排他锁并检查 pin。终态释放 pin，但保留成员与检查点供恢复；测试活跃阻止、终态允许 detach/drop。
4. 注册 create/list/read/cancel 四权限，增加列表和幂等取消接口。取消与目标写入同 job 锁顺序，提升 epoch，保留 checkpoint。ETA 从近十五分钟已提交批次的墙钟速率计算，样本不足返回 null。
5. 同步 M4 接口表、迁移精确集合断言；运行 `powershell -File scripts/verify.ps1 -Integration -Migration`，检查 TODO、敏感日志及函数/文件长度。

验收中修正：replay Redis 租约固定到期，不续租；grant 后续操作只使用剩余预算。成员冻结 CTE 同步写入 pin，并将冻结事务的 work_mem 限定为 64MB 以减少 50 万成员的临时文件开销；批次处置使用一次集合更新且精确检查受影响行数。既有漂移测试在各次 DB 查询前重新取得三秒上下文，不复用跨执行片段的过期句柄。
