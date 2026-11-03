# Backlog

| # | 发现于 | 问题 | 影响 | 建议里程碑 |
|---|---|---|---|---|
| 1 | M0 | `.golangci.yml` 的 `disable-all: true` 是 golangci-lint v1 语法，v2 会移除；升级时改为 `linters.default: none` | 当前可用，升级 v2 时需调整配置 | 升级 golangci-lint v2 时处理，M0 不修改 |
| 2 | M3b T3.7/T3.9 | 已修复：发布前将 `status_tables` 快照写入最终 spec，再计算 checksum，事务内存库后 CAS active；worker 从 DB spec 独立编译 | `CompileFromVersion` 对无快照的旧版本返回 `ErrStatusSnapshotMissing`，须重新发布；不回退当前 sys_setting，无需迁移 | 验证见 `MappingSnapshotIntegration`：真实发布、独立 worker 进程、状态表修改/不可访问、重启 checksum 一致及 CAS 回滚 |
