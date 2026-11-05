# S1 · 超时预算可配置（M5 之前）

- 类型：工程任务，不属于业务里程碑
- 依赖：S0 已合并
- 预计：**0.5–1 人日**
- 分支：`chore/s1-timeout-budget`
- 完成标志：CI 集成测试连续 5 次运行零假红；生产默认值不变。

---

## 1. 现象

同一提交（`0ea4536`）触发两次 push run，一次全绿、一次失败：

```
--- FAIL: TestExportIntegrationAdmissionConcurrent (3.58s)
    Error: Received unexpected error: (details: timeout: context deadline exceeded)
```

历史上同类：

| 时间点 | 失败用例 | 分片 |
|---|---|---|
| M3 | bootstrap 集成测试 | 全量 |
| S0 PR #8 | `TestCatalogCategorySyncIntegrationTreeRerunMappingAndStaleCycle` | catalog |
| S0 PR #8 | `TestExportIntegrationAdmissionConcurrent` | export |
| S0 合并后 | `TestExportIntegrationAdmissionConcurrent` | export |

共同特征：耗时 3.5–4 秒，报 `context deadline exceeded`，重跑即过。

## 2. 根因

两个硬编码预算在 CI 负载下不够：

| 位置 | 现值 | 被谁用 |
|---|---|---|
| `internal/kernel/db` 事务预算 | 3 秒 | 每个事务；导出准入、类目多页同步会逼近上限 |
| `internal/kernel/cache` Redis 操作 | 1 秒 | 会话、授权缓存、outbox 租约 |

CI runner 比开发机慢 2–3 倍，且 5 个分片各自跑 Postgres + Redis，I/O 竞争更明显。**代码没有错，是预算按开发机标定的。**

## 3. 方案

| 方案 | 做法 | 优 | 劣 |
|---|---|---|---|
| A 调大硬编码值 | 3s→10s、1s→5s | 一分钟改完 | 生产也会变慢，掩盖真实性能退化 |
| **B 配置化 + 测试覆盖** | 加配置项，默认保持现值；测试环境用大值 | 生产不变、CI 稳定、可审计 | 需要加 config 字段与传递路径 |
| C 只加大测试用例的 ctx | 改几个测试 | 改动最小 | 治标；新测试会重蹈覆辙 |

**决策：B**。

## 4. 任务

| # | 任务 |
|---|---|
| S1.1 | `internal/kernel/config` 增加 `db.txn_timeout`（默认 3s）与 `cache.op_timeout`（默认 1s），env 分别为 `CH_DB_TXN_TIMEOUT`、`CH_CACHE_OP_TIMEOUT` |
| S1.2 | `kernel/db` 与 `kernel/cache` 从 config 读取，删除硬编码；**默认值必须与现在完全一致** |
| S1.3 | `internal/testenv` 在集成测试环境提供 15s / 5s 的覆盖值（仅当 `TEST_DATABASE_URL` 存在时） |
| S1.4 | `scripts/verify.ps1 -Integration` 自动注入覆盖值，无需手工 export |
| S1.5 | 文档：`docs/04-conventions.md` 或配置说明里记录这两个项的语义与生产默认值 |

## 5. 硬约束

- **生产默认值不得改变**：3s / 1s 保持不变，否则是行为变更而非工程改进
- 不得放宽任何断言来让测试通过（禁止改 `require.Less(elapsed, 3*time.Second)` 这类容量断言）
- 不得给测试加 `time.Sleep` 或重试循环来掩盖超时
- 配置项读取失败时用默认值并记录一次告警，不 panic

## 6. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 默认值不变 | 不设 env 时，打印出的 txn 预算 = 3s、Redis op = 1s |
| 2 | 覆盖生效 | 设 `CH_DB_TXN_TIMEOUT=15s` 后，同一测试的预算为 15s |
| 3 | 假红消失 | 导出准入、类目同步两个用例各连跑 20 次，0 失败（贴命令） |
| 4 | 全量绿 | `scripts/verify.ps1 -Integration -Migration` 通过 |
| 5 | CI 稳定 | 连续 5 次 CI run 全绿（可推送空提交触发，或观察后续 5 次） |
| 6 | 无行为变更 | `git diff` 中生产路径只有「读取配置」的替换，无逻辑变化 |

## 7. 不做

- 不改任何业务逻辑、不加缓存、不加重试
- 不动容量类断言（50 万冻结、10 万导出）
- 不引入新的超时库

## 8. 可粘贴的 Codex prompt

```
实现 docs/tasks/S1-timeout-budget.md。只做超时预算配置化，不改业务逻辑。

必读：本卡、internal/kernel/db（事务预算处）、internal/kernel/cache（Redis 操作处）、
internal/kernel/config、internal/testenv、scripts/verify.ps1

任务：
1. config 增加 db.txn_timeout（默认 3s）与 cache.op_timeout（默认 1s），
   env 为 CH_DB_TXN_TIMEOUT / CH_CACHE_OP_TIMEOUT，支持 Go duration 字符串。
2. kernel/db、kernel/cache 改为读 config，删除硬编码常量。
   默认值与现值必须完全一致。
3. testenv 在集成测试环境提供 15s / 5s 覆盖；verify.ps1 -Integration 自动注入。
4. 配置解析失败 → 用默认值 + 一条告警日志，不 panic。

验收：
- 不设 env 时打印 3s / 1s（贴输出）
- CH_DB_TXN_TIMEOUT=15s 时生效（贴输出）
- go test ./internal/bootstrap -run 'ExportIntegrationAdmission|CatalogCategorySync' -count=20 零失败（贴汇总行）
- scripts/verify.ps1 -Integration -Migration 通过
- git diff 生产路径只有配置读取替换

完成后停下报告，不要开始 M5a。
```
