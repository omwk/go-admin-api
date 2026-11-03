# Backlog

| # | 发现于 | 问题 | 影响 | 建议里程碑 |
|---|---|---|---|---|
| 1 | M0 | `.golangci.yml` 的 `disable-all: true` 是 golangci-lint v1 语法，v2 会移除；升级时改为 `linters.default: none` | 当前可用，升级 v2 时需调整配置 | 升级 golangci-lint v2 时处理，M0 不修改 |
| 2 | M3b T3.7/T3.9 | M3a 发布端口只返回内存中的状态表快照，`mapping_version.spec` 的 DSL 类型未包含冻结快照，worker 无法从 DB 重建使用 `map_status` 的发布版本 | 此类版本重编译时拒绝执行；本批 mock 验收使用直接映射，不读取可变状态表代替原快照 | 映射发布持久化批次：按 ADR-016 Q1 明确快照持久契约，再补发布、重启和原版本重放测试 |
