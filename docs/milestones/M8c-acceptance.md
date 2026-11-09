# M8c · 开放网关验收收尾

- 日期：2026-11-10；分支 `feat/M8c-gateway-hardening`，基线 `5a9bbaf`（M8b）。
- 规格：ADR-022 第 1–10 条、M8-review Q1–Q6/§7、本次 M8c 任务。
- 本批交付：签名/错误/运维文档、bash 与 Go 示例、三个真实演练、S3 第 15 步、显式容量测量与本验收记录。
- **性能目标未通过**：单并发额外开销 p99=58.560ms，高于 20ms；已按任务要求提供瓶颈分析与后续方案。M8 到本批停止，原 PRD 未落地部分保留为明确遗留；不启动 M9。

## REQ-GW-001～009 对照

下表区分 ADR-022 后实际发布能力与原 PRD 完整范围，不将已有缺口记作通过。引用测试均在现有普通测试或 `verify -Integration` 中执行；M8c 没有扩展生产接口或修改既有裁决。

| 需求 | 当前能力与证据 | 结论 / 边界 |
|---|---|---|
| REQ-GW-001 路由注册 | `gateway.Capabilities/registeredRoute`、`gatewayStore.SaveRoute`；`TestGatewayHTTPIntegrationDoubleGateAndAuthorization`、`TestOpenContractMatchesCapabilitiesAndExactFields`；S3 经管理 API 登记 `order.status` | 已交付代码登记 + DB 启停的内置 handler。任意 HTTP 代理、前缀动态匹配及超时/重试/缓存配置未交付；不能承诺任意新增路由只改 DB |
| REQ-GW-002 应用管理 | 应用 owner/IP 精确白名单/状态/version、随机 secret 一次性发放、密钥元数据；`TestGatewayHTTPIntegrationManagementAndSecretReceipts`、`TestGatewaySecretIntegrationCipherAndSuspension`；S3 真正创建应用及授权 | 当前边界通过；secret 不进入列表、幂等回执或日志。HMAC 不能代替管理 JWT/RBAC |
| REQ-GW-003 签名 | `gateway.CanonicalRequest/Authenticator`；`TestHMACAuthenticationMatrix`、`TestHMACReplayAndRotation`、`TestExampleSignatureMatchesServerProtocol/RejectsAmbiguousQuery`；三个演练均实际执行 bash/Go 示例并获得 200 | 六段串、精确 body 字节、base64 文本 key、±300s、应用共享 nonce/TTL 600s 通过。重复 query 拒绝；nonce 先占位再验签，错误签名不退占位 |
| REQ-GW-004 授权与配额 | 路由 grant 与逐订单公开 ULID、owner 当前权限/范围；`TestGatewayHTTPIntegrationResourceCeilingAndLiveOwnerScope`、`TestGatewayQuotaIntegrationUTCDayMonthAndRestart/ConcurrentAtomicAdmission/PolicyReductionAndApplicationIsolation`；quota 演练分别验证日/月 N=2 与 N+1 | 本批通过。日/月计数是应用总准入量，轮换/重启不赠送额度；准入后业务失败仍占配额，拒绝不占。未启用钱包/套餐 |
| REQ-GW-005 分层限流 | 全局、应用总、路由及应用×路由原子 Redis Lua 桶；`TestGatewayTokenBucketsConcurrentAdmission/LongestBlockingWaitAndIdleTTL`、`TestGatewayRateHTTPIntegrationRetryAfterAndOtherApplication` | 共享 key/跨进程决策与拒绝不部分扣桶通过；租户暂等同全局，不启用每 key 桶。阈值当前由统一配置提供，未开放逐应用/逐路由 DB 策略覆盖；容量见独立报告 |
| REQ-GW-006 可见性 | private/disabled/付费/未登记能力统一不可用；独立 DTO/OpenAPI 精确白名单；`TestGatewayHTTPIntegrationPublicContractAndMarkers/DoubleGateAndAuthorization`、`TestOpenContractMatchesCapabilitiesAndExactFields` | 当前公开边界通过；仅状态 `id/order_no/status/updated_at`，无平台/店铺/内部 ID、金额、PII；不公开候选完整订单/价格 |
| REQ-GW-007 调用日志 | `api_call/api_call_log/api_call_meter` 原子终结、服务端 call_id、真实 usage；`TestGatewayMeteringIntegrationSingleTerminalAndUsage/TransactionRollbackAndLostAcknowledgement/RecoveryDoesNotBill`、`TestGatewayMeteringHTTPIntegrationDisconnectAfterCommitStillMeters`；S3 SQL + usage 对账 | 持久计量/日志及本应用 usage 通过。管理端按应用/路由/时间查询日志 API 未交付；req_bytes 仅当前无 body GET 的 0，resp_bytes 为持久结果 JSON 大小，并非完整线上字节数；不宣称原 PRD 全量检索/字节观测完成 |
| REQ-GW-008 上游保护 | 当前只有本地规范读；`CallService` 5s 总预算、GET/HEAD 最多两次、非幂等不自动重试、结果 ≤2MiB、错误收敛；`TestCallExecutionLogicalIdentityAndFailures`、`TestCallRetrySuccessAndUnknownCommit`、`TestGatewayMeteringHTTPIntegrationRetriesKeepOneIntent/FailuresNeverMeter` | 以上执行保护通过；HTTP Transport/SSRF/重定向/熔断/流式与解压后响应上限未交付，不能以本地读测试代替代理验收 |
| REQ-GW-009 密钥轮换 | 两 key 上限/事务锁、短重叠窗口、主库吊销；`TestGatewaySecretIntegrationRotationRevocationAndExpiry/ConcurrentIssuance`；rotation/revocation 两个真实 API 演练 | 通过：窗口内旧/新 200，窗口后旧 401/新 200；吊销提交后第一次新签名请求 401。nonce/限流/配额仍按应用共享 |

## 文档与可执行签名示例

入口为 [接入说明](../gateway/README.md)、[签名与编码](../gateway/signing.md)、[错误与重试](../gateway/errors.md)、[轮换/吊销/配额操作手册](../gateway/operations.md)。生产契约为 `internal/modules/gateway/openapi.yaml`，当前前缀是 `/open/v1`；历史 `/api/v1/open/**` 和 `/ping` 示例只属于旧规划，不是发布地址。

401/80101、80102、80103 沿用 M8a 已接受的统一文案“应用凭证不可用”；依赖故障 503 不伪报凭证失败。429 的公开 details 仅 `limit_type`，复位时间由 UTC 日/月边界及 `Retry-After` 表达，不擅自增加 reset_at 响应字段。balance 仍为 503/10002；usage 查询不计量、不占业务配额。

S3 第 15 步用管理 JWT 创建真实应用、登记既有能力并授权一条已经同步的订单；之后 open 调用不携带管理 JWT。独立签名 helper → HTTP 200 → 用服务端 call_id 查一条 succeeded 日志和一条 meter → 分别执行 Go SDK 风格命令及 bash/curl 文件 → usage 恰为三次成功、三次计量、零 processing。文档链接到这些实际执行文件，避免维护另一份签名算法。

## 三个演练与 15 步冒烟

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/gateway/rotation.ps1 -RedisAddr 127.0.0.1:16379
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/gateway/revocation.ps1 -RedisAddr 127.0.0.1:16379
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/gateway/quota.ps1 -RedisAddr 127.0.0.1:16379
```

| 脚本 | 本次输出证据 | 结果 |
|---|---|---|
| rotation | `PASS: dual-key rotation, both keys inside window; old=401/new=200 after window` | 15/15 PASS，34.69s，exit=0 |
| revocation | `PASS: leak response, first post-revocation request=401; replacement=200` | 15/15 PASS，30.35s，exit=0 |
| quota | `daily_quota HTTP=429 code=10005 ... reset_at=2026-11-10T00:00:00Z Retry-After=3485`；`monthly_quota ... reset_at=2026-12-01T00:00:00Z Retry-After=1817884`，两者均 PASS | 15/15 PASS，31.25s，exit=0 |

日/月 quota 演练均核对准入数停在 2、usage calls=3/metered_calls=2；复位输出与服务端 Retry-After 的 UTC 边界误差不超过 3 秒。UTC 测试时间为 2026-11-09 晚间，对应本地日期 2026-11-10，所以日复位为上述 UTC 2026-11-10。

三轮都输出 `PASS: open signature -> HTTP 200 -> durable meter/log -> usage; bash and Go examples verified`。原 1～13 步保留；第 15 步在 finally 的清理第 14 步之前执行。每轮清理均确认 `remaining_schemas=0; remaining_redis_keys=0` 且停止所有自有进程。基础独立入口为 `powershell -NoProfile -ExecutionPolicy Bypass -File scripts/smoke.ps1 -RedisAddr 127.0.0.1:16379`，演练脚本复用同一完整 15 步流程。

额外执行 `powershell -NoProfile -ExecutionPolicy Bypass -File scripts/smoke.ps1 -RedisAddr 127.0.0.1:16379 -Fault SkipRevoke`：故意省略分享吊销，旧浏览请求实际得到 200，脚本在第 13 步正确 FAIL 并 exit=13；第 15 步明确 SKIPPED，第 14 步清理 PASS、剩余 schema/Redis key 均为 0，总耗时 26.13s。外层验证断言该预期退出码后输出 `PASS: intentional fault rejected at step 13; cleanup still executed`，没有修改产品或测试断言制造失败。

## 压测验收

[报告](M8c-performance.md)和[原始 JSON](M8c-performance.json)记录 direct/open/配对差值的 p50/p95/p99、并发档位、QPS、样本、零错误与测量边界。2000 次/档、1/4/16/32 并发，单并发额外 p99=58.560ms，最高观测 open=123.47 QPS；计量 DB 阶段 p99=24.062ms，认证主库含 nonce p99=16.773ms，限流 Redis p99=1.038ms，HMAC 批均值 p99=0.002535ms。主瓶颈为持久计量/配额事务及生命周期主库写入，高并发有共享行热点与连接排队。

20ms 目标未通过；报告提出 SQL/事务往返、锁等待/连接池观测和 last_used_at 与安全门禁分离的待评审方案，没有减少持久化或放松吊销。容量测试有 build tag + 环境变量双开关，PR CI 没有时延/容量/统计断言。

## 仓库验收与自检

本次使用仓库 `scripts/verify.ps1 -Integration -Migration` 执行 Makefile 等价目标：`go vet ./...`、`golangci-lint run`、`go test ./...`、真实 PostgreSQL 全集成测试和 CLI migrate up/down/up，exit=0。没有将本机未执行的 `make` 或 race 记作已执行。

```text
$ powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1 -Integration -Migration
go vet ./...: passed
golangci-lint run: passed
go test ./...: passed
  scripts/gateway/client 1.050s
  scripts/smoke (cached)
go test -count=1 -p 1 ./internal/kernel/db ./internal/bootstrap ./cmd/cli -run Integration:
  internal/kernel/db 5.468s
  internal/bootstrap 206.517s
  cmd/cli 10.563s
== migrate up/down/up ==
version=18 dirty=false
version=17 dirty=false
version=18 dirty=false
all checks passed
exit=0
```

另外执行 `golangci-lint run --build-tags gatewaybench ./internal/bootstrap`，exit=0，输出 `gatewaybench tagged lint PASS`，确保普通构建之外的压测代码同样受 lint 检查。

- 分片守卫：`powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-test-groups.ps1`；`Source 317 = shards 316 + excluded 1`；Gateway 25 个集成测试恰好归属 gateway；最后一行 `PASS: every integration test has exactly one shard or an explicit exclusion.`
- 空实现：`rg -n 'TODO|not implemented' internal`，无命中。
- 格式/长度：本批 6 个 Go 文件 `gofmt -l` 无输出；Go AST 检查 `files=6 max_file=193 max_function=60`；6 个 PowerShell 文件 ASCII/parser PASS，bash `-n` PASS，`git diff --check` 通过。长度命令：`go run $env:TEMP/m8c-length.go -- scripts/smoke/transport.go internal/bootstrap/gateway_benchmark_test.go internal/bootstrap/gateway_benchmark_http_test.go scripts/gateway/client/client.go scripts/gateway/client/client_test.go scripts/gateway/example/main.go`。
- 敏感输出检查：签发/轮换返回只存内存，helper 输入为捕获管道，示例 secret 只走子进程环境；输出仅安全 label、HTTP/code、request_id、限额/时间。保存的 HTTP 证据不含 body/头/密钥。压测 JSON 不含连接串、应用/订单标识或 secret。本批不新增生产日志、不改迁移或依赖。
- 新增接口：无；现有公开接口及 M8b quota 管理扩展记录在 M8 任务卡接口表。

### DoD 自检

- [x] lint 等价目标通过：`golangci-lint run`、压测 tag lint 均 exit=0，验证脚本最后一行 `all checks passed`。
- [x] test 等价目标通过：`go test ./...` 全部通过；全集成汇总见上方三包输出。
- [x] 迁移 up/down/up 通过：实际执行 `go run ./cmd/cli migrate up`、`go run ./cmd/cli migrate down`、`go run ./cmd/cli migrate up`，18→17→18，均 dirty=false。
- [x] 接口清单已核对：M8c 无新增接口；M8 卡补录 M8b 已有 usage/balance/quota 扩展。
- [x] `rg -n 'TODO|not implemented' internal` 无命中，无空实现或伪造余额/计量。
- [x] 人工检查所有新增输出点，并扫描本次压测/演练/verify 捕获日志的 Authorization、X-Signature 和 secret/token JSON 标记，无敏感输出命中。
- [x] 文件/函数长度、PowerShell ASCII/parser、bash 语法、gofmt 和 whitespace 检查通过，具体命令与最大值见上。

## 偏差与遗留

1. 性能未达到 20ms，按本次任务要求保留实测与瓶颈方案，不能标为性能通过。
2. 原 PRD 的 HTTP 代理/完整上游保护、管理日志检索、`/open/v1/routes`、逐路由限流策略覆盖及完整字节观测未完成，见逐项矩阵；当前交付保持 ADR-022 的最小订单状态公开能力。本批没有越界补实现。
3. 本机 `CGO_ENABLED=0`、无 gcc，未运行 race；该结论需合并前 CI 证明。本批停在本地实现与验收报告，未创建发布标签，未开启 M9。
