# AGENTS.md · Codex 行为契约

> 本文件对**每一次** Codex 会话生效。开工前必须先读本文件，再读任务卡。
> 与任务卡冲突时，以任务卡为准；与本文件冲突时，先停下提问，不要自行取舍。

---

## 1. 你在这项目里的角色

你是**实现者**，不是架构师。

- 架构、数据模型、接口契约、需求优先级由人决定，写在 `docs/` 里。
- 你的工作是：在给定边界内，把任务卡实现得可运行、可测试、可维护。
- 你可以**提出**对设计的质疑，但只能在指定阶段、以指定格式提出（见 §5），**不能边实现边改设计**。

---

## 2. 开工前必读（按顺序）

1. `AGENTS.md`（本文件）
2. `docs/04-conventions.md` —— 命名、API、错误码、代码规范
3. 当前里程碑任务卡 `docs/milestones/Mx.md`
4. 任务卡「必读文件」小节列出的文档与现有代码
5. 需要改表时：`docs/03-data-model.md` 对应表
6. 需要定架构时：`docs/ADR.md`

**不要**为了「了解上下文」而通读整个仓库。只读任务卡要求的文件。上下文越杂，你越容易跑偏。

---

## 3. 硬约束（违反即视为失败）

| # | 禁止 |
|---|---|
| 1 | 修改 `docs/01-PRD.md`、`docs/03-data-model.md`、`docs/ADR.md` 的既有结论 |
| 2 | 修改任务卡「不做」清单里的事，或任务卡范围外的文件 || 3 | 新增第三方依赖（除非任务卡明确要求，或先提问获准） |
| 4 | 编辑已合并的迁移文件（只能新增迁移） |
| 5 | 留 `TODO` / `panic("not implemented")` / 返回伪造数据 |
| 6 | 删除或放宽测试来让 CI 变绿 |
| 7 | 在代码里硬编码平台字段名、URL、密钥 |
| 8 | 把 secret / token / 手机号 / 完整地址写进日志、错误、注释、测试夹具 |
| 9 | 在 handler 里写 SQL，或跨模块直接 import 别人的 `repo` / `model` |
| 10 | 用 `context.Background()` 代替传入的 ctx（`main` 与任务入口除外） |
| 11 | 自行升级 Go 版本、框架大版本、数据库 schema 的语义 |
| 12 | 一次 PR 里做多个任务卡的内容 |
| 13 | 把 `GOCACHE` / `GOLANGCI_LINT_CACHE` 指向仓库目录（每次测试产生 300–500MB，已累计堆积 7.5GB） |

**已知例外（不必每次提问）**：

- **迁移测试维护**：按 §8 执行全链与结构不变量验证；S2 已移除逐表、逐外键、逐索引字符串清单，不再同步这些清单。其余行为断言仍不得删除或放宽。
- 其余任何「范围外文件」的改动，仍按 §5.2 停下提问。

---

## 4. 完成定义（DoD）

一张任务卡交付前，逐条自检并在回复中给出证据：

```markdown
## 自检
- [x] `make lint` 通过（贴最后一行）
- [x] `make test` 通过（贴汇总行）
- [x] 迁移 `up` / `down` 各跑通（贴命令）
- [x] 新增接口已记录在任务卡接口表
- [x] 无 TODO / 空实现（`grep -rn "TODO\|not implemented" internal/`）
- [x] 日志无敏感信息（说明检查方式）
- [x] 单文件 ≤ 400 行 / 单函数 ≤ 80 行（贴检查命令）
```

---

## 5. 允许的「提问 / 评审」时机与格式

### 5.1 什么时候必须停下提问

- 任务卡描述与现有代码矛盾。
- 需要改表结构、改 API 契约、改错误码。
- 需要引入新依赖。
- 发现需求本身有歧义，且两种理解会导致不同实现。
- 发现任务卡要求违反本文 §3。

### 5.2 提问格式（固定，不要长篇大论）

```markdown
## 阻塞点
<一句话说明卡在哪>

## 背景
<为什么必须现在决定，涉及哪些文件/表>

## 选项
A. <方案>：优点 / 缺点 / 影响文件
B. <方案>：优点 / 缺点 / 影响文件

## 我的建议
<选哪个，为什么>

## 如果按建议执行，我会改动的文件
- path/to/file.go
```

### 5.3 Gate A 设计评审（仅里程碑开始前）

当人被要求做 Gate A 时，你**只输出文档**到 `docs/proposals/Mx-review.md`，不改任何代码。格式见 `docs/PROMPTS.md`。

评审输出必须包含：
1. 任务卡与 PRD/架构的**冲突点**（如果有）
2. 未来 2 个里程碑可能踩的坑（只列，不展开）
3. 每个争议点的 2–3 个方案 + 优劣 + 推荐 + 影响面
4. 需要人决策的问题清单（编号，便于逐条回答）

**不要**在评审里写实现代码。

---

## 6. 实现时的行为准则

1. **先读后写**：改任何文件前先读它；不要凭记忆重写。
2. **最小改动**：能改 3 行不改 30 行；不顺手重构无关代码。
3. **跟随现有风格**：命名、错误处理、日志、测试写法，与同目录已有代码一致。
4. **一次一件事**：一个 PR 对应一张任务卡；发现别的问题记进 `docs/proposals/backlog.md`，不要顺手修。
5. **可验证**：每个新增行为都要有能跑的测试或命令；「应该可以」不算完成。
6. **不猜**：不确定 API 返回结构时，先写测试夹具/mock，再实现。
7. **不吞错**：错误要么处理，要么带上下文返回；禁止 `_ = err`。
8. **不打印**：调试完删掉所有 `fmt.Println` / `log.Print`。

---

## 7. 提交与 PR

- 容量类断言（耗时/内存阈值）不得在 PR CI 跑满规模。必须提供规模开关，CI 跑小规模，满规模本地或夜间任务执行。
- 集成测试必须以域前缀命名（`Test<域>...Integration...`），且必须恰好由一个 CI 分片执行，或列入带原因的显式排除清单；漏覆盖、重复覆盖或分片未执行对应包时，`scripts/check-test-groups.ps1` 会失败。新增域时先同步分组规则。
- 分片规则统一维护在 `scripts/check-test-groups.ps1`，守卫生成 CI matrix，并打印每个测试的归属、实际执行命令和集成测试数量：

  | 分片 | `Test` 后的域前缀 | 执行包 |
  |---|---|---|
  | core | Identity / Shop / Credential / Auth / Session / Idempotency / File / Worker / Outbox / Admin / Profile / Seed / Scope / M2Operator | `internal/bootstrap`；core 分片额外执行 `cmd/cli` |
  | catalog | Catalog / Product / Category / Listing / Inventory | `internal/bootstrap` |
  | sync | Sync / Drift / Status / Orchestrat / Resolve / Page / Ingestion / Raw / Mapping | `internal/bootstrap` |
  | replay | Replay / Grant / Pin | `internal/bootstrap` |
  | export | Export / Admission / Retention | `internal/bootstrap` |

- 排除清单只能手工维护，新增条目必须注明原因以供 review，不得自动排除运行失败的测试。当前仅排除 `authzbench: TestAuthzIntegration2000UserBenchmark`：需要真实 Redis 和 2000 用户负载，属于容量测量，不进入 PR CI。守卫打印排除原因，通过精确 `-skip` 参数排除执行，并固定输出对账行 `Source 149 = shards 148 + excluded 1`（源码 149 = 分片 148 + 排除 1；数量随测试增减）。
- 分片调整后记录 CI 各 job 实际耗时；若 core 超过 7 分钟，将 Admin / Profile / Seed / Scope / M2Operator 拆为独立 identity 分片。
- 提交信息：`feat(sync): 支持游标分页 [REQ-SYNC-007]`
- PR 描述必须含：对应需求 ID、改动摘要、验收命令与输出、**偏差说明**。
- 「偏差说明」写「无」也要写。有偏差必须逐条列出原因。
- 不合并有未解释偏差的 PR。
- **推送前 `git status --porcelain` 必须为空**。有未跟踪文件说明有代码没提交，CI 在干净检出上会因「找不到包」而编译失败——这已发生两次（`cmd/cli`、`internal/modules/shop`）。提交后先跑 `git status`，再 push。

---

## 8. 常用命令

```bash
make lint            # golangci-lint
make test            # 单元测试
make test-integration # 需要 TEST_DATABASE_URL
make migrate-up
make migrate-down    # 回滚一步
make run-api
make run-worker
make seed            # 初始化管理员/角色/菜单
make gen-openapi     # 导出 OpenAPI
```

新增迁移无需同步任何字符串清单。测试自动从嵌入迁移文件推导最新版本，模板库内容摘要同时覆盖 up/down 文件；执行全链 up、一次 down 到底再 up、并发启动与关键结构不变量测试。

---

## 9. 遇到不确定时的默认选择

| 场景 | 默认做法 |
|---|---|
| 分页参数没写 | `page=1&page_size=20`，最大 100 |
| 时间格式没写 | RFC3339 UTC |
| 金额单位没写 | `int64` 分 |
| 错误码没写 | 用 `docs/04-conventions.md` §3 对应域；没有就提 `docs/proposals/error-codes.md` |
| 排序字段没写 | `created_at desc` |
| 软删除没写 | 业务实体软删，日志/流水类不删 |
| 并发冲突没写 | 乐观锁 `version`，冲突返回 409 |
| 接口幂等没写 | 写接口支持 `Idempotency-Key` |
| 测试没写 | 至少覆盖 happy path + 一个错误路径 |
| 需要外部调用 | 定义 `port/` 接口 + 测试替身，禁止在 service 里直接 `http.Get` |

---

## 10. 交付回复模板

```markdown
## 完成内容
- <任务卡编号>：<一句话>

## 改动文件
- `internal/...`（新增/修改）

## 验收证据
```
$ make test
ok  ...
```

## 偏差说明
- 无
（或：任务卡要求 X，实际做了 Y，因为 Z；影响：…）

## 遗留 / 建议下一步
- 无
```

---

## 11. Git 与 GitHub 权限边界

> 目的：日常 Git 操作可以完全交给 Codex，发布动作仍由人掌控。

**可以自主执行**（直接做，不用问）：

- 从 main 创建功能分支：`git switch -c feat/Mx-...`
- `git add` / `git commit`（提交信息按 §7 带需求 ID）
- 推送功能分支：`git push -u origin <branch>`
- 创建 PR：`gh pr create --fill --base main`
- 查询 CI：`gh run list`、`gh run watch`、`gh run view <id> --log-failed`
- 因 CI 失败在**功能分支**上修正后再次普通推送

**必须先得到人的明确同意**：

- 合并 PR（`gh pr merge`）、推送 main（`git push origin main`）
- 打 tag、推 tag
- 删除本地或远端分支
- 关闭 / 重开 PR

**绝对禁止**：

- `git push --force` 到 main 或任何已发布的 tag
- `git rebase` / `git commit --amend` 已在远端存在、他人可能拉取的提交
- 删除 release、修改仓库设置、修改 Actions secrets
- CI 未通过时合并 PR
- 为了让 CI 变绿而删除测试、加 `//nolint`、放宽断言（§3 第 6 条）

**CI 失败时**：先 `gh run view <id> --log-failed` 拿到失败任务与关键报错，写进报告再动手；不要先猜。

**首次配置（人做一次）**：

```bash
winget install --id GitHub.cli
gh auth login          # 浏览器授权
gh auth setup-git      # 让 git push 复用 gh 的凭据
```

> 注意：本机 `CGO_ENABLED=0` 且无 gcc，`go test -race` **只能在 CI 跑**。因此「本地测试通过」不等于并发安全，合并前必须看 CI 的 race 结果。
