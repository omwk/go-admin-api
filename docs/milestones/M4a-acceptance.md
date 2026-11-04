# M4a 验收与交接

- 日期：2026-11-04；分支：`feat/M4a-catalog`。
- 本轮：REQ-PRD-003、REQ-PRD-004，库存快照、类目映射管理与接口文档收尾。
- 状态：本轮专项测试与完整 `verify.ps1 -Integration -Migration` 已通过；交付后停在 M4a 人工验收，不启动 M4b。
- 没有新增依赖、修改既有迁移或修改 PRD / 数据模型 / ADR 的既有结论。

## 1. 本轮逐项验收

| 要求 | 可运行证据 | 结果 |
|---|---|---|
| 页事务中每个成功写入的 listing_sku 追加快照 | `TestCatalogInventoryIntegrationSyncSnapshots` | 首次同步 3 个 SKU，快照也是 3 行；同来源重投不追加；新来源更新原 SKU 后追加快照，SKU 总数不增加 |
| available / reserved | 同上 | available 与平台 stock 一致；未报告 reserved=0；映射 SKU `ext.reserved=2` 时快照保留 2 |
| synced_at 使用本次同步时间 | 同上 | 输入 24 小时前的 SourceFetchedAt，快照时间仍不早于本次操作开始时间 |
| 失败页整体回滚 | `TestSyncIntegrationPageRollbackPreservesRaw` | 失败页快照为 0；沿用原 raw/listing/cursor 回滚断言 |
| 每日清理严格早于 30 天的数据 | `TestCatalogInventoryIntegrationRetentionAndScopedReads` | 31 天前一行删除，恰好 30 天与新行保留（3→2）；当天不重复清理；次日旧边界行删除（2→1） |
| 库存查询 tenant / shop scope | 同上及映射 HTTP 测试 | 跨租户拒绝、无店铺权限为 0；SKU 过滤、分页、401、非法 page_size 覆盖 |
| SPU 汇总及口径 | `TestCatalogProductIntegrationMergeSummaryAndSyncPriceRefresh` | 三条 listing 库存和为 3，平台更新一条为 2 后汇总为 4；详情 `reported_stock` 标注「可能重复计算」 |
| 未匹配 / 已匹配 / 全部池 | `TestCatalogMappingIntegrationPoolManualFilterAndCandidates` | 初始未匹配 20；人工指定一条后 false=19、true=1、all=20；其他租户 0 |
| 人工指定 source=manual | 同上 | 同租户 active 类目校验；source=manual、confidence=1；缺映射返回 40401 |
| 人工操作幂等 | 同上 | 相同请求重放不改变首次时间；相同 key 不同请求返回 10006 |
| 商品使用映射类目过滤 | 同上 `assertMappedProductFilter` | 写入平台 category_id 并绑定 SPU，实际 GET `/products?category_id=...` 返回该商品 |
| 自动规则仅候选，不覆盖 manual | 同上及 `TestCatalogCategorySyncIntegrationTreeRerunMappingAndStaleCycle` | 实际 POST 返回 0.9 候选，另一条精确匹配仍未赋值；已有 manual 完整保持；父规则 0.6 候选及陈旧周期回归 |
| OpenAPI 补齐本轮端点 | `TestCatalogCategoryOpenAPIRoutesMatchManifest` 及实际 HTTP schema 校验 | 映射 GET / PUT / POST、库存 GET 路由双向校验，成功响应 schema 校验；商品详情增加 reported_stock，列表增加映射类目说明 |
| 迁移 000007 完整 down / up | `TestMigrateIntegrationReversibleAndRepeatable` | 精确校验 8 张表全部消失和恢复，含 export_job；版本与总表数 7/47→6/39→7/47 |

### 数据口径与运行入口

- 快照与 listing、listing_sku 共用同步页事务。来源仲裁跳过、隔离或失败回滚的记录不计入成功写入快照数。
- `available` 为平台报告的 SKU `stock`。平台报告预留量时由映射输出 SKU `ext.reserved`；不报告则为 0。
- `synced_at` 是本次 Upsert 的 UTC 时间，来源仲裁继续使用原来源时间 / 顺序 / checksum。
- worker 既有 leader 调度循环接入 `inventorySchedule`：启动时补清，之后每个 UTC 日执行一次 `synced_at < now - 30d`；失败不会标记当天完成，下次调度重试。
- 商品详情 `reported_stock=sum(全部关联 listing_sku.stock)`，可能重复计算，不代表实物库存。
- listing 的映射通用字段 `category_id` 保存为 `ext.category_id` 字符串；商品筛选实时关联已应用的 category_mapping，人工指定后无需重新同步商品。
- `GET /category-mappings` 默认 matched=false，接受 true / all；可选 platform_id。`POST /category-mappings:auto` 接受 JSON `{}` 或 platform_id，分页返回未匹配记录及候选，不写映射。

## 2. 对照 M4 任务卡

此表覆盖整张 M4 卡，区分现有 M4a 证据与未纳入本轮的项目；不能据此宣称整个 M4 已完成。

| 任务 | M4a 证据 / 状态 |
|---|---|
| T4.1 迁移、model、索引 | 000007 的 8 张表及 down/up 集成回归通过；迁移内索引保留。关键 SQL 的 EXPLAIN 现场证据未采集，见 T4.10 |
| T4.2 listing / SKU 落库 | 同步页事务、来源新旧与 checksum 仲裁、重投不重复 SKU；本轮加入同事务库存快照 |
| T4.3 SPU / SKU CRUD、人工归并 | 现有归并回归把 3 条 listing 挂到 SPU；人工标题不被同步覆盖，SKU 绑定保留，汇总重算正确 |
| T4.4 商品列表 / 详情 | 100000 商品、25 次首页调用 P95=92.5994ms；page=100 后连续游标 5 页共 100 个不同商品；跨租户与安全投影回归 |
| T4.5 类目同步、映射、候选 | 完整树重跑、manual 保留、陈旧周期、父候选；本轮池过滤、人工指定、候选只读和商品类目过滤通过 |
| T4.6 库存快照、清理 | 本文 §1 的行数、时间、报告数量、边界与 scope 测试 |
| T4.7 历史重放执行器 | 按 M4 拆分属于 M4b；本轮未实施或验收 |
| T4.8 单条重同步 | 本轮请求未包含该接口；尚无实现 / 验收证据，不标完成，后续批次须确认归属 |
| T4.9 异步导出 | 按 M4 拆分属于 M4b；未实施。export_job 已随既有 000007 建表，本轮仅验证其回滚，不新增 000008 |
| T4.10 索引 / 慢查询 | 有首页 P95 自动断言；未采集服务端 log_min_duration_statement=200ms 日志或关键查询 EXPLAIN，不以时延测试替代这两项证据 |

### 对照 M4 §9 验收清单

| # | 项目 | 证据 / 限制 |
|---|---|---|
| 1 | 十万列表 P95 <500ms | 上述真实 PostgreSQL 集成测试，25 次串行 service 调用；不是 ab 200 次、并发 10 的 HTTP 压测 |
| 2 | 深分页 | 签名游标测试拒绝 page>100，校验 tenant / filter；page=100 后 keyset 5 页无重复 |
| 3 | 人工归并 | 3 条 listing 归并、SKU 数与价格汇总、平台同步刷新回归 |
| 4 | 类目 | 池数量与实际 HTTP 商品过滤通过 |
| 5 | 重放 | M4b，未开始 |
| 6 | 单条重同步 | 未包含本轮，未验收 |
| 7 | 幂等 | 同来源重复写入无重复 SKU / 快照；人工映射幂等重放与请求冲突覆盖 |
| 8 | 导出 | M4b，未开始 |
| 9 | EXPLAIN 无 seq scan | 未采集；不标通过 |
| 10 | 冗余字段 | SKUCount / 价格范围在归并、人工替换、平台更新后均有断言 |

M4 接口表中的内部类目 POST / PUT 也未包含在本轮的「类目映射管理」范围内；不将其记为已完成。

## 3. 验证命令与输出

缓存均位于系统临时目录，不在仓库中。实际 PostgreSQL 连接来自被忽略的本机配置，不在文档或日志中记录连接串。

```powershell
$env:GOCACHE = Join-Path $env:TEMP 'go-admin-api-go-build'
$env:GOLANGCI_LINT_CACHE = Join-Path $env:TEMP 'go-admin-api-lint'
./scripts/verify.ps1 -Integration -Migration
```

完整验证退出码为 0，最终输出：

```text
== go vet ==
== golangci-lint ==
== go test ==
ok  github.com/omwk/go-admin-api/internal/bootstrap 168.597s
（其余测试包全部通过，部分命中缓存）
== integration ==
ok  github.com/omwk/go-admin-api/internal/kernel/db 3.878s
ok  github.com/omwk/go-admin-api/internal/bootstrap 166.671s
ok  github.com/omwk/go-admin-api/cmd/cli 8.836s
== migrate up/down/up ==
version=7 dirty=false
version=6 dirty=false
version=7 dirty=false
all checks passed
```

`TestMigrateIntegrationReversibleAndRepeatable` 还检查 7→6 时 8 张 catalog 表全部消失，含 export_job，6→7 后精确恢复；随后继续验证更早版本的全链路回滚。

专项命令及已取得的输出：

```text
go test ./internal/bootstrap -run 'TestCatalog(Inventory|Mapping)Integration' -count=1
ok  github.com/omwk/go-admin-api/internal/bootstrap 10.479s

go test ./internal/bootstrap -run 'TestOrchestrationRepositoryReflectionMatrix|TestCatalogProductIntegration' -count=1 -v
100000 products, 25 first-page requests: P95=92.5994ms
ok  github.com/omwk/go-admin-api/internal/bootstrap 14.918s
```

## 4. 自检与偏差说明

- 本机无可用 make；使用仓库 `verify.ps1` 执行 go vet、golangci-lint run、go test ./... 和串行 Integration，等价项如实记录，不宣称运行过 make。
- 第一次全量验证发现 Catalog 查询注册精确矩阵仍包含旧 AutoApplyCategoryMappings。已更新为实际的 CleanupInventorySnapshots / ListInventories 清单，保持 Equal 精确集合断言。
- `rg -n 'TODO|not implemented' internal/` 无匹配；`git -c core.safecrlf=false diff --check` 无输出。
- Go AST 检查本轮修改及新增的 20 个 Go 文件：单文件 ≤400 行、单函数 ≤80 行，violations=0；命令 `go run (Join-Path $env:TEMP 'm4a-length-check.go')`。
- 日志检查：审阅全部新增代码与 diff，没有新增日志输出；同步回归保留敏感信息不外泄断言，HTTP 响应验证安全投影。
- 无 Go 版本、依赖、schema 语义变更；既有 000007 down 原本已包含 export_job，本轮加强精确回滚断言。
- 本机 CGO_ENABLED=0、无 gcc，race 需在 CI 取得证据；本次未运行 CI、未提交、推送、合并或打 tag。
- 停止点：M4a 人工验收；T4.10 现场证据及表中未验收项保留明确状态，不自动开启 M4b。
