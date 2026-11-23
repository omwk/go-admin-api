# BE-IDN-029 验收记录

基线：`main@98b28f7`；分支：`feat/be-idn-029-gateway-observability`。

接口：`GET /api/v1/admin/api-calls`、`GET /api/v1/admin/api-calls/summary`、`POST /api/v1/admin/api-clients/{id}/try`。

专项原始输出：[acceptance-tests.txt](acceptance-tests.txt)；并发检测：[race.txt](race.txt)；全量日志：[verify.txt](verify.txt)。

| # | 验收项 | 已执行的证据 |
|---|---|---|
| 1 | 日志筛选与分页 | `TestGatewayObservabilityIntegrationListFiltersPrivacy`：默认 total=4；client+route+success 总数=2，page=2/page_size=1 命中 exact-1；failed=2；显式 48h 范围=5；客户端名与路由 code 一致。 |
| 2 | 时间范围 | 同测试：25h 前记录不进入默认窗口；31 天、非法时间、非法分页和状态均为 HTTP 400。单元测试同时验证恰好 30 天可用。 |
| 3 | trace 精确匹配 | exact-2 命中 1 行，前缀 exact 命中 0 行。 |
| 4 | 隐私 | 对 list 内每行断言不存在 request_body/response_body/result/ip_hash/secret/cipher_text；SQL 显式列投影；试调持久结果为 `{}`，无响应体留存。 |
| 5 | client/route/day 统计 | `TestGatewayObservabilityIntegrationSummarySQL`：三种分组均 calls=4、success_rate=0.50、p95_latency_ms=38.5、cost_cents=28；processing 样本 latency=999 被排除。独立 SQL 与手算见下。 |
| 6 | 试调成功 | `TestGatewayObservabilityIntegrationTrialNoBilling`：真实 HMAC v2、真实授权与订单 reader，status_code=200、含响应与 latency_ms；api_call_log 新增 1 行 manual_test，管理查询 test=true。 |
| 7 | 试调不计费 | 同测试启用真实计费端口、路由 per_call/7 分；`SELECT count(*) FROM api_call_meter`：before=0，after=0；call_charge/wallet_txn/unit_txn/api_quota 均 0 行。 |
| 8 | 未授权结构化结果 | `TestGatewayObservabilityIntegrationTrialGates`：外层 HTTP 200，data.status_code=403、error_code=80104；同时覆盖路由停用/私有、客户端停用、密钥吊销、IP 白名单拒绝与日/月配额耗尽。 |
| 9 | 响应截断 | `TestGatewayObservabilityIntegrationTrialTruncation`：真实 open handler 配测试 reader 返回超过 2400000 字节的 Unicode 响应，status_code=200、truncated=true、保留 262144 字节，持久结果仍为 `{}`。 |
| 10 | 租户隔离 | `TestGatewayObservabilityIntegrationTenantAndPermissions`：普通租户查询其他租户日志不可见，client filter/summary/trial 为 404；配置的平台超管可观测跨租户日志。`TenantSuperBoundary` 验证租户 2 超管仍不能读取/试调租户 1 客户端。 |
| 11 | OpenAPI 双向一致 | `TestGatewayObservabilityContractBidirectional`：三个 operation/permission 映射、全部 DTO 字段双向核对。`IntegrationContractResponses`：真实 list/summary/trial 与 401 响应符合 [gateway.yaml](../../../../api/openapi/gateway.yaml)。 |
| 12 | 全量 verify | `powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1 -Integration` 退出 0，末行为 `all checks passed`。vet、golangci-lint、go test ./... 与完整数据库集成测试全部通过。 |

## 第 5 项 SQL 与手算

在独立测试数据库、client_id=1 上执行：

```sql
SELECT count(*) AS calls,
       sum(CASE WHEN status_code BETWEEN 200 AND 299 THEN 1 ELSE 0 END)::float8
         / count(*) AS rate,
       array_agg(latency_ms ORDER BY latency_ms) AS samples,
       sum(cost_cents) AS cost
FROM api_call_log
WHERE tenant_id=1 AND client_id=1 AND finished_at IS NOT NULL;
-- calls=4, rate=0.5, samples={10,20,30,40}, cost=28
```

状态码样本 `[200,204,500,403]`：成功率 `2/4=0.5`。延迟升序 `[10,20,30,40]`：连续分位秩 `1+(4-1)*0.95=3.85`，插值 `30+(40-30)*0.85=38.5ms`。每条费用 7 分，合计 `4*7=28`。三个 HTTP 分组逐一断言这些结果。

## 第 7 项 SQL 前后计数

```sql
SELECT count(*) FROM api_call_meter; -- before: 0
-- POST /api/v1/admin/api-clients/1/try
-- {"route_id":1,"path":"/open/v1/orders/{public_order_id}/status","body":null}
-- response: status_code=200, billing.mode=per_call, billed=false
SELECT count(*) FROM api_call_meter; -- after: 0
SELECT count(*) FROM call_charge;    -- 0
SELECT count(*) FROM wallet_txn;     -- 0
SELECT count(*) FROM unit_txn;       -- 0
SELECT count(*) FROM api_quota;      -- 0
```

配额耗尽测试预置 admitted_count=1、limit=1，试调返回 429/10005，计数仍为 1；日/月各验证一次。外部请求添加 X-Gateway-Test/X-Test 不会启用试调：普通调用仍写 1 条 meter、日/月各扣 1 次。

## 实现映射与范围

- 既有 state 为 processing/succeeded/failed；卡片 finished 对应 `finished_at IS NOT NULL`，保持既有状态模型。
- 既有计费模式为 free/per_call；响应沿用该值。请求支持设计稿中的可选 method/path；订单状态路由需提供包含公开订单 ID 的具体 path。
- 手动试调跳过批量收费准入函数，使用同一调用事务检查配额但不增减；成功/失败均写 manual_test 日志；公网请求无法构造私有 context 标记。
- 新权限追加到目录末尾，保留既有位置；沿用完整目录的超管 seed。菜单投影显式登记后端范围豁免，前端另卡。
- 无新增依赖、无数据库结构变更、无前端改动。

## 清理

已清理：本卡专项测试的独立 `commercehub_test_*` 数据库由 `t.Cleanup` 执行 `DROP DATABASE IF EXISTS`；清理错误会使测试失败，专项测试全通过。开发库未写入本卡测试租户、客户端、调用日志或审计；系统配置未修改。共享迁移模板由仓库测试运行时管理，无本卡业务数据留存。

## 全量与工程检查

```text
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1 -Integration
ok github.com/omwk/go-admin-api/internal/bootstrap 434.441s  # go test ./...
ok github.com/omwk/go-admin-api/internal/kernel/db 8.649s  # integration
ok github.com/omwk/go-admin-api/internal/bootstrap 423.820s # integration
ok github.com/omwk/go-admin-api/cmd/cli 38.101s             # integration
all checks passed

go test -race ./internal/modules/gateway ./internal/bootstrap -run '^Test(GatewayObservability|Observation|ManualTrial)' -count=1 -v
ok github.com/omwk/go-admin-api/internal/modules/gateway 6.755s
ok github.com/omwk/go-admin-api/internal/bootstrap 44.753s

go test ./internal/bootstrap -run '^TestGatewayMigrationIntegrationUpDownUp$' -count=1 -v
migration 17 -> 16 -> 40; dirty=false
PASS

powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-test-groups.ps1
Source 664 = shards 663 + excluded 1
PASS: every integration test has exactly one shard or an explicit exclusion.
```

源码标准 Go AST 检查（本机临时脚本 `go run "$env:TEMP/be-idn-029-sizes.go" -- <git diff/ls-files 得到的 Go 文件>`）：`max_file_lines=322 max_function_lines=80`，见 [source-checks.txt](source-checks.txt)。新增生产代码无 TODO/空实现/调试输出/替换传入 ctx 的 Background 调用；`git diff --check` 通过。新接口由显式 DTO/SQL 列白名单及隐私回归测试覆盖；新代码不记录请求体、响应体或签名密钥。

首次全量验证暴露了权限目录插入位置与菜单投影登记问题，原始失败保留于 [verify-initial.txt](verify-initial.txt)。已修复为目录末尾追加，并为两个后端权限登记具体的前端另卡原因；既有断言未删除或放宽，最终全量通过。
