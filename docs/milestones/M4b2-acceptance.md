# M4b2 第一轮验收记录

日期：2026-11-05。分支：`feat/M4b2-export`。需求：REQ-PRD-008，ADR-018 §5/§8。

## 完成范围

- `000009_export_rows` 可逆迁移；唯一主键 `(export_id, attempt, ordinal)` 同时支持排序 keyset。
- 创建任务冻结 filters/sort/字段版本/tenant/owner；worker 开始时一次 INSERT SELECT 物化完整白名单投影；重试复制原快照。
- 每批最多 1000，io.Pipe 流式 CSV 和私有 multipart 上传；大小、行数和 SHA-256 读回校验后，CAS 发布唯一 DB 指针。
- 新 SDK 适配器仅接导出，minio-go/v7 固定 v7.0.83，Go 1.23，旧 HTTP 适配器保留。
- attempt 独立键、tenant/owner 下载校验、剩余寿命限制、引用保护清理、超过 1h 的残留扫描、outbox 清理重试和 CSV 公式转义。
- 两个 API 与 RBAC、OpenAPI、幂等创建、worker dispatch 已装配。任务卡接口表已存在并同步说明。

## 专项验收

```powershell
$env:GIN_MODE='release'
go test -count=1 ./internal/bootstrap -run 'TestExportIntegration|TestSyncDriftIntegrationWarmupContinuationAndPageDetection' -v
```

真实 PostgreSQL。真实 S3 使用本地隐藏运行的临时 MinIO：固定源码标签 `RELEASE.2024-12-18T13-15-44Z`，对应 `v0.0.0-20241218131544-16f8cf1c52f0`，Go 校验和 `h1:VkIkrCKtbftWOcvIN45eXM5Y+BHo6kLrH3bRf2TBrvg=`。官方二进制站返回 410，改从校验过的 Go 模块缓存构建到临时目录；未加入项目依赖。随机凭据仅在进程环境传递，临时服务和数据在验收后清理。

复跑真实 S3 测试需在环境中提供 `CH_STORAGE_ENDPOINT`、`CH_STORAGE_ACCESS_KEY`、`CH_STORAGE_SECRET_KEY`；测试创建独立私有桶并清理。缺凭据会显式 skip。本次提供了凭据，S3 测试实际执行通过。

| 验收 | 证据 |
|---|---|
| 创建冻结过滤条件，开始后修改数据仍导出开始内容 | `FrozenContentAndRetry` PASS；更新 live 商品后，重试投影和 generated_at 与首次完全一致 |
| 10 万行与数据库一致，无重复 | `HundredThousandRows` PASS：物化 986.2331ms，总计 1.6611475s，数据行 100000 |
| 真实 multipart 上传、读回校验、发布与下载 | `S3HundredThousandDownloadAndExpiry` PASS：物化 923.6301ms，总计 1.6538123s，下载 100000 数据行 |
| 中途 kill 后重启 | `KillWorkerDuringUploadAndRestart` PASS；真实子进程开始流式写入后强杀，重启得到 attempt-2、2001 行原快照 |
| 两 attempt 并发，只有 DB 指针对象可下载 | `ConcurrentAndLateAttempts/overlapping_uploads` PASS；旧对象删除，仅一个 file_object 登记 |
| 旧 attempt 迟到不覆盖新结果 | `ConcurrentAndLateAttempts/late_old_upload` PASS；下载目标与内容不变 |
| URL 过期 403、对象默认私有 | 真实 MinIO：签名 GET 200；匿名 GET 403；1 秒签名过期 GET 403；剩余寿命签名 ≤30 秒 |
| 跨 tenant/owner 404 | `HTTPIsolationIdempotencyAndValidation` PASS，200/404 响应通过 OpenAPI 校验 |
| 清理保留被引用 attempt-1，删除存在的 attempt-2 | `CleanupProtectsReferencedOldAttempt` PASS |
| 崩溃残留扫描和持久重试 | `CleanupOrphansAgeAndRetry` PASS；新临时键保留、超过 1h 临时键删除、无关前缀保留，重试不增加待办 |
| CSV `= + - @` 转义 | HTTP 集成与 `TestExportCellPreventsFormulaInjection` PASS；含前置空白的公式同样加单引号 |
| 大小/摘要损坏禁止发布、空快照、幂等 | `DigestFailureAndEmptySnapshot`、`Idempotency` PASS |
| worker delivery lease 覆盖 5 分钟硬预算 | `DeliveryLease` PASS，export 6 分钟，其他事件维持 60 秒 |

```text
PASS
ok github.com/omwk/go-admin-api/internal/bootstrap 33.503s
```

## 全量验证

在同一临时真实 MinIO 环境执行：

```powershell
powershell -File scripts/verify.ps1 -Integration -Migration
```

```text
== go vet ==
== golangci-lint ==
== go test ==
ok github.com/omwk/go-admin-api/internal/bootstrap 260.701s
ok github.com/omwk/go-admin-api/internal/kernel/db 6.385s
== integration ==
ok github.com/omwk/go-admin-api/internal/kernel/db 4.702s
ok github.com/omwk/go-admin-api/internal/bootstrap 245.988s
ok github.com/omwk/go-admin-api/cmd/cli 8.443s
== migrate up/down/up ==
version=9 dirty=false
version=8 dirty=false
version=9 dirty=false
all checks passed
```

退出码 0。迁移测试保留表/外键全集断言，新增物化字段、主键与 down 保留 started_at 的断言。首次全量运行发现遗漏的并发迁移版本断言，已同步至 9；此前一次同步测试在构建 MinIO 并行负载中超时，单独及最终全量复跑均通过，未修改该测试或放宽断言。

## 自检与偏差

- vet、lint、全部测试及可逆迁移通过（Windows 对应 verify 入口，未使用 make）。
- `rg -n 'TODO|not implemented' internal` 无匹配；`git diff --check` 通过；变更 Go 文件 `gofmt -l` 无输出。
- 逐项检查新增日志和存储错误：清理日志只有固定 module/action，不输出对象 URL、凭据、投影、raw 或请求内容。
- `go run ./bin/m0-verify/_tools/sizecheck.go`：`files=370 max_file_lines=359 functions=1680 max_function_lines=93`，退出 1，唯一违反项为既有 `internal/modules/identity/release.go:Apply: 93 lines`。该文件无改动，本轮新增/修改函数均符合 ≤80 行；已登记 backlog。
- 本机 CGO_ENABLED=0，无 race 运行证据；并发时序集成通过，race 仍须 CI 验证。
- 第二轮的并发准入、pending 限额、观测阈值及其他保留期管理尚未实施；本轮不宣告完整 M4b2 里程碑交付。

## 第二轮完成记录

日期：2026-11-05。需求：REQ-PRD-008，ADR-018 §5/§6/§8。M4b2 两轮实现和本地验收已完成；race 结果仍需 CI 验证。

### 完成内容与改动文件

- `internal/bootstrap/catalog_export_admission.go`、`catalog_export.go`、`catalog_export_rows.go`：单事务持有全局 `pg_advisory_xact_lock(1163415636,1)`，计数后创建 running 预留；worker 启动/失败重试走同一锁复核。全局≤2、租户≤1、请求人≤1，租户 pending≥10 拒绝。幂等重放先返回原结果；超限 HTTP 429/10005，响应 message/details 指明限制层。拒绝不落 job/dispatch；Redis 授权故障 HTTP 503，不绕过 DB 限制。
- `internal/modules/catalog/export_cleanup.go`、`export_types.go` 与 `internal/bootstrap/catalog_export_cleanup.go`：24h 到期同事务标 expired、清除发布指针/文件元数据、登记精确键 outbox，删除前重读当前 DB 指针与 attempt fence。清理扫描继续处理其他对象，同键未完成待办去重，重试复用原 outbox。保留已引用 attempt，临时/崩溃残留超过 1h 才清理。
- `internal/bootstrap/catalog_export_metrics.go`、`catalog_export_observation.go`、`scope.go`、`worker.go` 和 `cmd/api/main.go`：DB collector 注册到 `/metrics`，提供四项要求指标；失败标记与每个完成 attempt 的耗时写入原清理 outbox，成功重试不抹掉失败计数。另提供执行/清理年龄，worker 每 5 秒检查执行 180/300 秒和清理 300/900 秒，清理连续重试 3/10 次告警，级别变化时写 Warn/Error。
- `api/openapi/catalog.yaml`、`docs/milestones/M4.md`、`docs/proposals/M4b2-plan.md`：同步 running 预留及 429/503 契约、清理与指标说明。
- 新增三组集成测试：`catalog_export_admission_integration_test.go`、`catalog_export_retention_integration_test.go`、`catalog_export_metrics_integration_test.go`。

### 专项验收

```powershell
$env:GIN_MODE='release'
go test ./internal/bootstrap -run 'TestExport' -count=1
```

```text
ok github.com/omwk/go-admin-api/internal/bootstrap 33.548s
```

| 验收项 | 证据 |
|---|---|
| 10 个并发创建，全局恰好 2 running、同租户最多 1、其余 8 个 429 | `AdmissionConcurrent` PASS；同时断言 DB running=2、dispatch=2 |
| pending 10 与三层 running 限制返回 429/10005 | `AdmissionLayers` 四个子用例 PASS，响应包含对应限制层，并通过 OpenAPI 验证 |
| 重试重新竞争额度，Redis 故障不放行 | `AdmissionRetryAndRedisFailure` PASS；DB 仍拒绝超限，HTTP 503 |
| 对象发布 25h 后清理，未过期对象保留 | `RetentionAndDeleteRetry` PASS：DB expires_at 提前，删除失败仍持久 expired，引用解除；恢复后重试成功，新对象保留 |
| attempt-1 被引用、attempt-2 多余对象只删后者 | 既有 `CleanupProtectsReferencedOldAttempt` PASS |
| 列举后才发布对象，删除前重读保留新指针 | `CleanupRereadsPublication` PASS |
| 临时键超过 1h、失败入 outbox，重试不放大待办 | 既有 `CleanupOrphansAgeAndRetry` 与新增 `RetentionAndDeleteRetry` PASS |
| `/metrics` 四项指标 | `MetricsEndpointAndPersistentFailures` PASS：running{tenant="1"}=1、duration_count=2、failed_total=1、cleanup_pending=2；重复 Fail 不重复计数 |
| ADR-018 告警阈值 | `ObservationFakeClock` PASS：179/180/299/300/899/900 秒；验证 Warn/Error 和相同级别不重复告警；`ObservationConsecutiveDeleteFailures` 覆盖 3/10 次重试 |
| 原有快照、强杀恢复、迟到 attempt、10 万行、下载权限与公式转义 | 全部 `TestExport` 通过；真实 S3 用例由下面完整验收覆盖 |

### 全量验收

使用已有固定版本 MinIO 二进制（版本来源见第一轮记录），临时独立数据目录、随机凭据、动态本地端口，环境变量传递凭据。MinIO 隐藏运行，验证结束后已停止并删除本次临时目录。实际执行：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1 -Integration -Migration
```

```text
Temporary MinIO ready; starting full verification
== go vet ==
== golangci-lint ==
== go test ==
ok github.com/omwk/go-admin-api/internal/bootstrap 259.117s
ok github.com/omwk/go-admin-api/internal/modules/catalog 1.063s
== integration ==
ok github.com/omwk/go-admin-api/internal/kernel/db 4.819s
ok github.com/omwk/go-admin-api/internal/bootstrap 255.303s
ok github.com/omwk/go-admin-api/cmd/cli 8.460s
== migrate up/down/up ==
version=9 dirty=false
version=8 dirty=false
version=9 dirty=false
all checks passed
```

退出码 0。没有新增或修改迁移，没有新增第三方依赖。

### 自检与偏差说明

- [x] lint / 单元测试：Windows 等价 `verify.ps1` 入口通过；lint 后进入 `== go test ==`，完整命令最后一行 `all checks passed`。
- [x] 集成与迁移：完整命令通过，`go run ./cmd/cli migrate up` / `down` / `up` 为 9→8→9，dirty=false。
- [x] 接口文档：沿用任务卡两个导出 API；OpenAPI 已记录 running 预留与 429/503。
- [x] `rg -n 'TODO|not implemented' internal` 无匹配；`git diff --check` 通过；通过 `rg --files ... -g '*export*.go'` 枚举 Go 文件后运行 `gofmt -l` 无输出。
- [x] 日志检查：逐项阅读清理与观测日志，只输出固定 module/action、severity 和数值；无对象键、URL、凭据、请求体或商品投影。
- [x] 本轮新增/修改函数和文件满足 ≤80/≤400 行。`go run ./bin/m0-verify/_tools/sizecheck.go` 输出 `files=376 max_file_lines=359 functions=1700 max_function_lines=93`，退出 1，唯一违规仍为既有 `internal/modules/identity/release.go:Apply: 93 lines`（未改动，已在 backlog 登记）。
- 偏差：未使用 make，使用仓库规定 Windows verify；本机无 gcc、CGO_ENABLED=0，race 留 CI。长度检查的既有违规保留，未混入本次导出任务修复。功能范围无偏差。
- 指标依赖保留的 outbox 事件：当前没有事件清理任务；后续如引入 outbox 归档/删除，必须同时保留或汇总 `failed` / `duration_seconds` 统计，避免历史累计指标回退。
