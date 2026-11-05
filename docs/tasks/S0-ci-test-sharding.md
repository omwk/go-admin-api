# S0 · CI 测试分组（M4 之后、M5 之前）

- 类型：工程任务，不属于任何业务里程碑
- 依赖：M4b2 已合并
- 预计：**1–1.5 人日**
- 分支：`chore/s0-ci-test-groups`
- 完成标志：CI 从单 job 15 分钟降到分片后最慢 job ≤7 分钟；新增集成测试若未被任何分组匹配，CI 直接失败。

---

## 1. 背景

M4 结束时 CI 单次 **15 分 26 秒**，而且几乎全部时间在 `internal/bootstrap` 的集成测试（约 330 秒，含 schema 创建、迁移、种子）。M5 订单域会再加状态机、幂等补单、PII 三组集成测试，预计突破 20 分钟。

问题不只是慢：

- 一个包失败只报「bootstrap 失败」，要翻全量日志才能定位是哪个域
- 所有域串行，改一行 catalog 也要等 replay 和 export 跑完
- 逼近 `go test` 单包超时与 job 时限的安全边界

## 2. 方案对比

| 方案 | 做法 | 优 | 劣 | 工作量 |
|---|---|---|---|---|
| A 拆成多个包 | 把测试文件移到 `internal/bootstrap/catalogtest/...`，夹具抽到 `internal/testkit` | 结构最干净，真正并行编译 | 需要导出大量内部符号或改造夹具；回归风险高 | 4–6 人日 |
| B build tag 分片 | 每个域文件加 `//go:build integration_catalog` 等 | 编译期隔离 | 每个文件都要加标签，漏一个就跑错组或编译失败；夹具文件必须常驻 | 2–3 人日 |
| **C CI 分组（`-run` 正则）** | **不改代码**，CI 用 5 个 job，各自 `go test ./internal/bootstrap -run '<正则>'` | **零代码改动**；失败按 job 隔离；可随时增减组 | 每组仍编译整包（缓存后约 20–30 秒）；依赖测试命名纪律 | **0.5 人日** |
| D 只调超时/加机器 | 什么都不改 | 0 | 不解决定位与串行问题 | 0 |

**决策：C**。它解决 90% 的问题（反馈时间、失败隔离）而风险接近零。A 留到 M6 之后、业务稳定时再做。

## 3. 分组

按测试函数名前缀匹配（现有命名已经天然分域）：

| 组 | `-run` 正则 | 覆盖 |
|---|---|---|
| `core` | `Test(Identity\|Shop\|Credential\|Auth\|Session\|Idempotency\|File\|Worker\|Outbox)` | 身份、店铺、凭据、会话、幂等、文件、outbox |
| `catalog` | `Test(Catalog\|Product\|Category\|Listing\|Inventory)` | 商品、类目、归并、库存 |
| `sync` | `Test(Sync\|Drift\|Status\|Orchestrat\|Resolve\|Page\|Ingestion)` | 同步编排、漂移、状态 |
| `replay` | `Test(Replay\|Grant\|Pin)` | 历史重放 |
| `export` | `Test(Export\|Admission\|Retention)` | 异步导出 |

单元测试仍保留一个 `-race` job，只跑 `./internal/kernel/... ./internal/modules/...`。

## 4. 任务

| # | 任务 | 产出 |
|---|---|---|
| S0.1 | CI 增加 matrix：5 个 integration 分片 + 1 个 unit(race) | `.github/workflows/ci.yml` |
| S0.2 | 分片失败时 job 名带组名（`integration (catalog)`） | 同上 |
| S0.3 | **覆盖守卫脚本**：枚举 `bootstrap` 下所有 `func Test*Integration*`，若存在未被任何分组正则匹配的测试，退出码 1 | `scripts/check-test-groups.ps1` |
| S0.4 | 守卫接入 CI，排在分片之前 | 同上 |
| S0.5 | 在 `AGENTS.md` 记录命名约定：集成测试必须以域前缀命名，否则守卫失败 | `AGENTS.md` §7 |

## 5. 守卫脚本要点

```
1. 扫描 internal/bootstrap/*_test.go 与 cmd/cli/*_test.go
2. 提取 func Test\w*Integration\w*\(t \*testing.T\)
3. 对每个函数名，检查是否被 5 个分组正则中的至少一个匹配
4. 输出未匹配清单并 exit 1
5. 同时输出每组匹配到的测试数量（用于发现「某组突然空了」）
```

**为什么必须有守卫**：分组靠正则匹配，新增测试改名或用了没预料的前缀时，它会**静默地不跑**。一个不跑的测试比一个失败的测试危险得多。

## 6. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 分片有效性 | 5 个分片跑到的测试数之和 = 包内集成测试总数 |
| 2 | 覆盖守卫 | 故意新增一个 `TestZzzIntegration`（不匹配任何组）→ CI 失败并指名该测试 |
| 3 | 反馈时间 | 最慢分片 ≤7 分钟（当前约 330 秒总量，分片后应显著低于此） |
| 4 | 失败隔离 | 故意让 catalog 组一个用例失败 → 只有 `integration (catalog)` 红，其他组绿 |
| 5 | 无功能改动 | 产品代码零改动；`go test ./...` 本地仍全绿 |
| 6 | 文档 | `AGENTS.md` 有命名约定；`docs/progress.md` 记录分组表 |

## 7. 不做

- 不移动测试文件、不拆包、不改夹具
- 不动产品代码
- 不引入测试并行（`t.Parallel`）—— schema 隔离虽已存在，但引入并行会放大现有的 1 秒超时类问题，另议
- 不优化单测耗时

## 8. 可粘贴的 Codex prompt

```
实现 docs/tasks/S0-ci-test-sharding.md，只改 CI 与脚本，不改产品代码。

必读：本卡、.github/workflows/ci.yml、AGENTS.md §7、scripts/verify.ps1

任务：
1. 把 CI 的单个集成 job 拆成 5 个 matrix 分片（core/catalog/sync/replay/export），
   每组 go test ./internal/bootstrap -run '<正则>' -count=1 -p 1
2. 保留 unit(race) job：./internal/kernel/... ./internal/modules/...
3. 新增 scripts/check-test-groups.ps1：枚举 bootstrap 与 cmd/cli 下所有
   Test*Integration* 函数，校验每个都被某个分组正则覆盖；未覆盖则 exit 1，
   并打印每组匹配数量。
4. 守卫接入 CI，排在分片之前。
5. AGENTS.md §7 增加命名约定：集成测试必须以域前缀命名。

验收：
- 5 组测试数之和 = 集成测试总数（贴数字）
- 临时新增 TestZzzIntegration 验证守卫会失败（验证后删除）
- 最慢分片 ≤7 分钟
- 产品代码零改动（git diff --stat 只含 ci.yml / scripts / AGENTS.md）
```

---

## 9. 后续（不在本卡）

真正拆包（方案 A）留到 M6 之后。到那时业务模块稳定，夹具 API 也定型了，抽 `internal/testkit` 的收益才大于风险。现在做等于在流动的地基上改结构。
