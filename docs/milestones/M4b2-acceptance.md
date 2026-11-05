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
