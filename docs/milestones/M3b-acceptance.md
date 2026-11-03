# M3b 第三轮验收

范围：REQ-SYNC-001～010、012 中本轮的漂移、管理操作、Shopee fixture 与同步闭环；裁决以 ADR-016 Q4 为准。本轮不增加依赖、迁移或 Go 版本。

**Shopee 未与真实平台联调。** 五种平台响应使用本地脱敏 fixture，通过 httptest 验证签名、请求参数、分页、鉴权过期、限流和数据异常；不声称生产平台可用性。

验收命令（PowerShell）：

```powershell
powershell -File scripts/verify.ps1 -Integration
```

| 验收项 | 可运行证据 |
|---|---|
| 原始路径/类型集合、数组归一、稳定摘要 | `TestStructureCanonicalTypesAndFingerprint` |
| 必填子字段缺失，不被兄弟元素掩盖 | `TestStructureDriftAndRequiredCollectionElements` |
| 基线包含多种类型时按页并集比较，不把单条子集误报为类型漂移 | `TestSyncDriftIntegrationPageTypeUnionDoesNotMisclassifyRecords` |
| 三周期后建立 active；partial 续跑不多算；第四周期每页检测 | `TestSyncDriftIntegrationWarmupContinuationAndPageDetection` |
| 空周期、Fetch 失败、隔离 partial 不参与预热 | `TestSyncDriftIntegrationFailedEmptyAndQuarantinedCyclesExcluded` |
| 隔离记录原始字段也参与；critical 事件/inbox/隔离/游标同事务；失败保留 raw | `TestSyncDriftIntegrationRawQuarantineAndNotificationAtomicity` |
| 跨店铺首例、baseline/kind/path 滚动 24h 去重 | `TestSyncDriftIntegrationRollingTenantDedupAcrossShops` |
| 五个新权限发布后生效，重复发布不恢复撤权，ignore 白名单 | `TestSyncDriftIntegrationAdminReleaseAndActions` |
| 人工 promote、旧基线 superseded、审计失败回滚、幂等一次审计 | `TestSyncDriftIntegrationPromoteAuditRollbackAndIdempotency` |
| 无身份/跨租户/跨店铺管理操作拒绝，列表 count 不泄漏 | `TestSyncDriftIntegrationAdminScopeDenials` |
| 5 页 ×100，raw/listing/SKU 各500无重复；真实已发布 map_status 冷编译；修改状态表结果不变 | `TestSyncIntegrationPublishedStatusSnapshot500Records` |
| 未知状态隔离 invalid_value，保留既有合法 listing | `TestSyncIntegrationPublishedStatusSnapshot500Records` 与 Shopee `unknown_status` fixture |
| API 不运行，独立 worker 子进程读取 DB spec，checksum 重启前后一致 | `TestMappingSnapshotIntegrationPublishAndWorker` |
| Shopee normal / expired / rate_limited / missing_required / unknown_status | `TestShopeeFixtures`（含真实映射 apply） |
| 网络/响应错误与重定向不泄漏凭据 | `TestShopeePaginationAndTransportSafety` |
| run/条目结果、游标延迟、drift open | `TestSyncIntegrationMetricsEndpoint`、`TestSyncDriftIntegrationWarmupContinuationAndPageDetection` |
| 所有新仓储方法反射登记、OpenAPI 权限一致 | `TestOrchestrationRepositoryReflectionMatrix`、`TestSyncOpenAPIRoutesAndPermissionsMatchManifest` |

新权限发布沿用 `go run ./cmd/cli permissions grant --role <角色码> --codes sync:drift:list,sync:drift:ack,sync:drift:resolve,sync:drift:ignore,sync:baseline:promote --release m3b-drift` 的一次性发布流程；词典可用 `go run ./cmd/cli permissions export` 导出。默认种子不用于恢复既有角色授权。

基线按租户/平台/资源分组。前三周期并集存入 active；后续有变化的完整周期存为 superseded 候选，人工以候选 ID promote。不会在结束时扫描历史 raw。失败页不提交结构或事件，重试使用已提交 run.stats 和 cursor。原始报文值不写入指纹、事件或通知，通知只含安全标识。

本地环境无 gcc，race 验证需由 CI 执行；本地 Integration 结果不能替代 CI race。

## 本轮校验记录（2026-11-03）

执行 `powershell -File scripts/verify.ps1 -Integration`，退出码 0。`go vet`、`golangci-lint run`、`go test ./...` 均通过，独立集成阶段输出：

```text
ok  github.com/omwk/go-admin-api/internal/kernel/db   3.239s
ok  github.com/omwk/go-admin-api/internal/bootstrap   141.482s
ok  github.com/omwk/go-admin-api/cmd/cli              10.315s
all checks passed
```

`git diff --check` 无错误；`rg -n 'TODO|not implemented|fmt\.Println|log\.Print' internal` 无匹配。对本轮变更 Go 文件运行 AST 尺寸检查，结果 `changed Go files <=400 lines; functions <=80 lines`。人工检查新增错误、事件、通知和日志路径，并通过 Shopee 错误回显/传输失败测试确认凭据不出现在错误中。无新增或修改迁移，未单独运行 `-Migration`；既有数据库集成测试通过。无依赖或 Go 版本变更。
