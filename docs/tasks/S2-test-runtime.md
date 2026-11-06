# S2 · 测试运行时重构（迁移只跑一次 + 迁移测试收敛）

- 类型：工程任务，不属于业务里程碑
- 依赖：S1（超时预算配置化）合并后
- 预计：**1.5–2 人日**
- 分支：`chore/s2-test-runtime`
- 完成标志：`bootstrap` 集成测试耗时下降 ≥50%；迁移测试从 6 个文件收敛到 2 个；删除的断言逐条说明理由。

---

## 1. 判断

当前测试体系是**按「长期维护的成熟项目」设计的**，但项目处在 9 个迁移、业务刚起步的阶段。具体表现：

1. **每个集成测试都从零跑完整迁移链**（9 个迁移 × 数十个测试）。这是主要成本，且随迁移数量线性增长。
2. **迁移测试断言与迁移 SQL 重复**。「000008 后 replay_run_item 存在」这类断言不防回归，只防手滑；手滑在 review 里就能看见。
3. **每个特性一套迁移测试文件**（ingestion / replay / export / session_index），加一个迁移要同步改多处。

结论：**成本在「重复执行」和「重复声明」，不在「测试覆盖」**。因此拆两件事做——先解决重复执行（模板库），再删重复声明（收敛断言）。

## 2. 现状盘点

| 位置 | 内容 | 问题 |
|---|---|---|
| `internal/kernel/db/integration_test.go` | 全链 up/down、表清单、外键清单、并发启动 | 表/外键清单需随迁移同步维护 |
| `internal/kernel/db/ingestion_migration_integration_test.go` | 000005 后分区与索引 | 与全链测试重复 |
| `internal/kernel/db/replay_migration_integration_test.go` | 000008 后表结构 | 同上 |
| `internal/kernel/db/export_migration_integration_test.go` | 000009 后表结构 | 同上 |
| `internal/kernel/db/session_index_integration_test.go` | 000003 索引 | 同上 |
| `internal/bootstrap/*_integration_test.go` | 每个测试建 schema + 全链迁移 + 种子 | **主要耗时来源** |

观测值（S0 分片后）：`bootstrap` 集成约 330 秒，`core` 分片 2 分 10 秒，`kernel/db` 迁移测试约 35 秒。

## 3. 保留 / 删除

### 保留（有真实回归价值）

| 断言 | 为什么留 |
|---|---|
| 全链 up：空库跑到最新版本 | 唯一真正重要的一条；迁移断裂会在这里暴露 |
| 一次 down 到底再 up | 回滚能力验一次足够，不必每个迁移都验 |
| 并发启动只允许一个实例迁移 | 测 advisory lock，存在真实竞态 |
| 分区表关键不变量（分区存在、跨月可建、默认分区行为） | 分区建错在业务层极难发现 |
| 关键唯一约束存在（租户内唯一、幂等键唯一） | 这些约束破了会静默写脏数据 |

### 删除（重复声明，无回归价值）

| 断言 | 为什么删 |
|---|---|
| 每个迁移后的「表名清单」逐条比对 | 迁移 SQL 即事实来源；清单只增加维护成本 |
| 每个迁移后的「外键清单」逐条比对 | 同上，且外键在 M1 后已固定 |
| 每个迁移后的「索引存在」逐条比对 | 需要索引时由查询性能或约束行为暴露 |
| 按特性拆分的四个迁移测试文件 | 与全链测试重复；保留其**非重复部分**并入不变量测试 |

> **删除必须逐条登记**：每个被删断言写一行「删了什么 / 原本防什么 / 为什么现在不需要」。这是本卡最容易敷衍的地方。

## 4. 模板库

### 目标

迁移只执行一次，每个测试克隆已迁移的库，而不是重跑迁移链。

### 方案

| 方案 | 做法 | 优 | 劣 |
|---|---|---|---|
| **A 模板数据库** | 启动时建 `commercehub_tpl` 并跑一次全链迁移；每个测试 `CREATE DATABASE test_x TEMPLATE commercehub_tpl`，结束时 drop | 克隆毫秒级；加迁移不再影响测试耗时 | 需要 `CREATEDB` 权限；DB 数量多 |
| B 模板 schema + `LIKE` 复制 | 迁移到 `tpl` schema，逐表 `CREATE TABLE ... (LIKE)` | 不需要 CREATEDB | 要自己复制索引/约束/分区，容易与真实结构漂移 |
| C 事务回滚 | 每个测试包在一个事务里，结束回滚 | 最快 | 破坏需要真实事务/并发的测试 |
| D 保持现状 | — | 0 | 成本随迁移线性增长 |

**决策：A，带权限回退**。若 `CREATE DATABASE ... TEMPLATE` 因权限失败，回退到现有「建 schema + 迁移」路径并**只告警一次**，不得让测试失败。

### 约束

- 模板库只含**结构**，不含种子数据（种子仍按测试各自创建）
- 模板库名固定前缀 `commercehub_tpl_<hash>`，hash 取迁移文件内容摘要；迁移变化时自动重建
- 测试结束必须 drop 测试库；异常退出时下次启动按前缀清理超过 1 小时的残留
- 同一进程内模板库只建一次（`sync.Once`）
- 连接池大小不得因多库而失控（每个测试库用完即关）

## 5. 任务

| # | 任务 |
|---|---|
| S2.1 | 实现模板库管理器（建模板、克隆、清理、权限回退） |
| S2.2 | `bootstrap` 与 `kernel/db` 夹具改用模板库；保留回退路径 |
| S2.3 | 迁移测试收敛到 2 个文件：全链与回滚、结构不变量 |
| S2.4 | 删除逐表/逐外键/逐索引清单断言，登记删除理由 |
| S2.5 | 更新 `AGENTS.md` §8：新增迁移不再需要同步任何字符串清单 |
| S2.6 | 输出耗时对比（改造前后各分片时长） |

## 6. 硬约束

- **不得减少业务覆盖**：只能删「重复声明」类断言，不能删行为测试
- 不得为了跑得快而跳过迁移（模板库必须由真实迁移链生成）
- 不得引入新的测试框架或依赖
- 回退路径必须真实可用（权限不足时测试仍能跑，只是慢）
- 删除清单必须逐条给理由，空话（如「已优化」）视为未完成

## 7. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 提速 | `bootstrap` 集成耗时 ≤165 秒（改造前约 330 秒）；贴前后对比 |
| 2 | 迁移只跑一次 | 日志显示模板库构建 1 次；克隆 N 次 |
| 3 | 回退可用 | 用无 `CREATEDB` 权限的角色运行，测试仍通过（只是慢） |
| 4 | 覆盖不减 | 业务行为测试数量不变；删除的仅为清单类断言，逐条列出 |
| 5 | 迁移测试收敛 | `internal/kernel/db` 下迁移相关测试文件 = 2 个 |
| 6 | 全量绿 | `scripts/verify.ps1 -Integration -Migration` 通过 |
| 7 | 稳定性 | 本地 `-count=20` 零失败 + CI 跑 1 次绿；合并后观察后续 3 个 PR 无假红（自然观测，不额外跑 CI） |

## 8. 风险

| 风险 | 缓解 |
|---|---|
| 模板库与真实迁移漂移 | 模板库必须由迁移链生成，且 hash 绑定迁移内容 |
| 测试库泄漏占满磁盘 | 启动时清理 1 小时前残留；失败路径也登记清理 |
| 权限差异导致本地过、CI 挂 | CI 与本地都用同一套回退逻辑；专门验一次无权限路径 |
| 删多了 | 删除清单逐条评审；行为测试数量作为硬指标 |

## 实施计划（2026-11-06）

- [x] 采集 bootstrap 基线与业务测试名称集合。
- [x] 在 `internal/testenv` 增加基于真实迁移回调的模板库管理器：内容摘要、跨进程锁、克隆、失败清理、过期清理、权限回退；以真实 PostgreSQL 测试隔离和无 CREATEDB 路径。
- [x] 修改 bootstrap 公共夹具及三个子进程重连夹具；kernel/db 行为夹具克隆，迁移测试使用空隔离空间。
- [x] 将迁移测试合并为全链与结构不变量两个文件，保留 S1 预算、锁、事务、审计行为；逐条登记删除项。
- [ ] 更新 AGENTS 迁移维护规则，运行 verify、count=20、范围检查；提交后确认干净再推送并等待 CI。

## 9. 可粘贴的 Codex prompt

```
实现 docs/tasks/S2-test-runtime.md。只改测试运行时与被删的清单断言，不改业务代码。

必读：本卡、internal/kernel/db/integration_test.go、
internal/bootstrap/identity_integration_helpers_test.go、internal/testenv、
scripts/verify.ps1、S1 引入的配置项

任务：
1. 模板库管理器：首次使用时建 commercehub_tpl_<migrations-hash> 并跑一次全链迁移；
   每个测试 CREATE DATABASE ... TEMPLATE；结束 drop；启动时清理 1 小时前残留。
2. 权限不足（无 CREATEDB）时回退到现有「建 schema + 迁移」路径，只告警一次。
3. kernel/db 迁移测试收敛为两个文件：
   - 全链 up + 一次 down 到底 + 再 up + 并发启动
   - 结构不变量（分区、关键唯一约束）
4. 删除逐表/逐外键/逐索引清单断言，逐条登记删除理由。
5. AGENTS.md §8 更新：新增迁移无需同步字符串清单。

验收：
- bootstrap 集成耗时 ≤165 秒（贴前后数字）
- 日志显示模板库构建 1 次
- 用无 CREATEDB 角色跑通（贴命令与结果）
- 迁移测试文件数 = 2
- 删除清单：每行含「删了什么 / 原本防什么 / 为什么不需要」
- scripts/verify.ps1 -Integration -Migration 通过
- 本地 -count=20 零失败；CI 跑 1 次绿；合并后观察后续 3 个 PR 无假红

完成后停下报告，不要开 M5a。
```

## 删除登记（基于 8394c68，逐项）

目录清单的事实来源是不可变的迁移 SQL；下表只删除目录/版本重复声明。事务、取消、审计、预算、并发锁以及 bootstrap 全部行为测试保留。

| 删了什么 | 原本防什么 | 为什么现在不需要 |
|---|---|---|
| 表集合成员 `audit_log` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `category` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `category_mapping` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `credential_audit` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `credential_version` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `dead_letter` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `export_job` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `export_job_row` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `file_object` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `idempotency_key` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `inventory_snapshot` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `listing` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `listing_sku` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `mapping_profile` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `mapping_version` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `notification` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `outbox_event` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `platform` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `platform_category` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `product_media` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `product_sku` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `product_spu` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `quarantine_record` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `raw_dedup` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `raw_partition_pin` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `raw_payload` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `raw_payload_<当月>` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `raw_payload_<次月>` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `replay_run` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `replay_run_item` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `schema_baseline` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `schema_drift_event` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `shop` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `shop_credential` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `sync_cursor` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `sync_error` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `sync_job` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `sync_run` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `sys_dept` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `sys_menu` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `sys_permission` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `sys_refresh_token` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `sys_role` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `sys_role_dept` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `sys_role_menu` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `sys_role_permission` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `sys_setting` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `sys_user` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `sys_user_role` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 表集合成员 `tenant` 的目录相等/阶段存在性比对 | 迁移遗漏该表或 down 未删表 | 重复迁移 SQL 的建表声明；完整链从空空间 up、down 到空、再 up 已验证链闭合，业务读写仍执行真实表。 |
| 外键 `fk_sys_menu_permission` → `sys_permission(tenant_id, code)` 的名称、引用列、租户列及 RESTRICT 定义比对 | 外键声明漏写或拼错 | 这是 SQL 文本的第二份声明；全链执行验证 DDL，租户隔离/关联删除业务测试保留，不维护外键字符串全集。 |
| 外键 `fk_sys_user_dept` → `sys_dept(tenant_id, id)` 的名称、引用列、租户列及 RESTRICT 定义比对 | 外键声明漏写或拼错 | 这是 SQL 文本的第二份声明；全链执行验证 DDL，租户隔离/关联删除业务测试保留，不维护外键字符串全集。 |
| 外键 `fk_sys_user_role_user` → `sys_user(tenant_id, id)` 的名称、引用列、租户列及 RESTRICT 定义比对 | 外键声明漏写或拼错 | 这是 SQL 文本的第二份声明；全链执行验证 DDL，租户隔离/关联删除业务测试保留，不维护外键字符串全集。 |
| 外键 `fk_sys_user_role_role` → `sys_role(tenant_id, id)` 的名称、引用列、租户列及 RESTRICT 定义比对 | 外键声明漏写或拼错 | 这是 SQL 文本的第二份声明；全链执行验证 DDL，租户隔离/关联删除业务测试保留，不维护外键字符串全集。 |
| 外键 `fk_sys_role_menu_role` → `sys_role(tenant_id, id)` 的名称、引用列、租户列及 RESTRICT 定义比对 | 外键声明漏写或拼错 | 这是 SQL 文本的第二份声明；全链执行验证 DDL，租户隔离/关联删除业务测试保留，不维护外键字符串全集。 |
| 外键 `fk_sys_role_menu_menu` → `sys_menu(tenant_id, id)` 的名称、引用列、租户列及 RESTRICT 定义比对 | 外键声明漏写或拼错 | 这是 SQL 文本的第二份声明；全链执行验证 DDL，租户隔离/关联删除业务测试保留，不维护外键字符串全集。 |
| 外键 `fk_sys_role_permission_role` → `sys_role(tenant_id, id)` 的名称、引用列、租户列及 RESTRICT 定义比对 | 外键声明漏写或拼错 | 这是 SQL 文本的第二份声明；全链执行验证 DDL，租户隔离/关联删除业务测试保留，不维护外键字符串全集。 |
| 外键 `fk_sys_role_permission_permission` → `sys_permission(tenant_id, id)` 的名称、引用列、租户列及 RESTRICT 定义比对 | 外键声明漏写或拼错 | 这是 SQL 文本的第二份声明；全链执行验证 DDL，租户隔离/关联删除业务测试保留，不维护外键字符串全集。 |
| 外键 `fk_sys_role_dept_role` → `sys_role(tenant_id, id)` 的名称、引用列、租户列及 RESTRICT 定义比对 | 外键声明漏写或拼错 | 这是 SQL 文本的第二份声明；全链执行验证 DDL，租户隔离/关联删除业务测试保留，不维护外键字符串全集。 |
| 外键 `fk_sys_role_dept_dept` → `sys_dept(tenant_id, id)` 的名称、引用列、租户列及 RESTRICT 定义比对 | 外键声明漏写或拼错 | 这是 SQL 文本的第二份声明；全链执行验证 DDL，租户隔离/关联删除业务测试保留，不维护外键字符串全集。 |
| 外键 `fk_shop_platform` → `platform(id)` 的名称、引用列、租户列及 RESTRICT 定义比对 | 外键声明漏写或拼错 | 这是 SQL 文本的第二份声明；全链执行验证 DDL，租户隔离/关联删除业务测试保留，不维护外键字符串全集。 |
| 外键 `fk_shop_owner` → `sys_user(tenant_id, id)` 的名称、引用列、租户列及 RESTRICT 定义比对 | 外键声明漏写或拼错 | 这是 SQL 文本的第二份声明；全链执行验证 DDL，租户隔离/关联删除业务测试保留，不维护外键字符串全集。 |
| 外键 `fk_shop_dept` → `sys_dept(tenant_id, id)` 的名称、引用列、租户列及 RESTRICT 定义比对 | 外键声明漏写或拼错 | 这是 SQL 文本的第二份声明；全链执行验证 DDL，租户隔离/关联删除业务测试保留，不维护外键字符串全集。 |
| 外键 `fk_shop_credential_shop` → `shop(tenant_id, id)` 的名称、引用列、租户列及 RESTRICT 定义比对 | 外键声明漏写或拼错 | 这是 SQL 文本的第二份声明；全链执行验证 DDL，租户隔离/关联删除业务测试保留，不维护外键字符串全集。 |
| 外键 `fk_credential_version_credential` → `shop_credential(tenant_id, id)` 的名称、引用列、租户列及 RESTRICT 定义比对 | 外键声明漏写或拼错 | 这是 SQL 文本的第二份声明；全链执行验证 DDL，租户隔离/关联删除业务测试保留，不维护外键字符串全集。 |
| 外键 `fk_shop_credential_current_version` → `credential_version(tenant_id, credential_id, id)` 的名称、引用列、租户列及 RESTRICT 定义比对 | 外键声明漏写或拼错 | 这是 SQL 文本的第二份声明；全链执行验证 DDL，租户隔离/关联删除业务测试保留，不维护外键字符串全集。 |
| 外键 `fk_credential_audit_credential` → `shop_credential(tenant_id, id)` 的名称、引用列、租户列及 RESTRICT 定义比对 | 外键声明漏写或拼错 | 这是 SQL 文本的第二份声明；全链执行验证 DDL，租户隔离/关联删除业务测试保留，不维护外键字符串全集。 |
| 外键 `fk_mapping_profile_platform` → `platform(id)` 的名称、引用列、租户列及 RESTRICT 定义比对 | 外键声明漏写或拼错 | 这是 SQL 文本的第二份声明；全链执行验证 DDL，租户隔离/关联删除业务测试保留，不维护外键字符串全集。 |
| 外键 `fk_mapping_version_profile` → `mapping_profile(tenant_id, id)` 的名称、引用列、租户列及 RESTRICT 定义比对 | 外键声明漏写或拼错 | 这是 SQL 文本的第二份声明；全链执行验证 DDL，租户隔离/关联删除业务测试保留，不维护外键字符串全集。 |
| 外键 `fk_mapping_profile_active_version` → `mapping_version(tenant_id, profile_id, id)` 的名称、引用列、租户列及 RESTRICT 定义比对 | 外键声明漏写或拼错 | 这是 SQL 文本的第二份声明；全链执行验证 DDL，租户隔离/关联删除业务测试保留，不维护外键字符串全集。 |
| 索引全集成员 `raw_payload_pkey` 的目录相等比对 | 索引被遗漏或重命名 | 名称不是业务契约；DDL 由真实迁移执行，关键唯一性另以重复写入失败验证，不保留索引目录全集。 |
| 索引全集成员 `uq_raw_dedup` 的目录相等比对 | 索引被遗漏或重命名 | 名称不是业务契约；DDL 由真实迁移执行，关键唯一性另以重复写入失败验证，不保留索引目录全集。 |
| 索引全集成员 `idx_raw_lookup` 的目录相等比对 | 索引被遗漏或重命名 | 名称不是业务契约；DDL 由真实迁移执行，关键唯一性另以重复写入失败验证，不保留索引目录全集。 |
| 索引全集成员 `idx_raw_replay_source` 的目录相等比对 | 索引被遗漏或重命名 | 名称不是业务契约；DDL 由真实迁移执行，关键唯一性另以重复写入失败验证，不保留索引目录全集。 |
| 索引全集成员 `idx_raw_unmapped` 的目录相等比对 | 索引被遗漏或重命名 | 名称不是业务契约；DDL 由真实迁移执行，关键唯一性另以重复写入失败验证，不保留索引目录全集。 |
| 索引全集成员 `uq_raw_directory` 的目录相等比对 | 索引被遗漏或重命名 | 名称不是业务契约；DDL 由真实迁移执行，关键唯一性另以重复写入失败验证，不保留索引目录全集。 |
| 索引全集成员 `schema_baseline_pkey` 的目录相等比对 | 索引被遗漏或重命名 | 名称不是业务契约；DDL 由真实迁移执行，关键唯一性另以重复写入失败验证，不保留索引目录全集。 |
| 索引全集成员 `uq_schema_baseline_active` 的目录相等比对 | 索引被遗漏或重命名 | 名称不是业务契约；DDL 由真实迁移执行，关键唯一性另以重复写入失败验证，不保留索引目录全集。 |
| 索引全集成员 `schema_drift_event_pkey` 的目录相等比对 | 索引被遗漏或重命名 | 名称不是业务契约；DDL 由真实迁移执行，关键唯一性另以重复写入失败验证，不保留索引目录全集。 |
| 索引全集成员 `idx_drift_open` 的目录相等比对 | 索引被遗漏或重命名 | 名称不是业务契约；DDL 由真实迁移执行，关键唯一性另以重复写入失败验证，不保留索引目录全集。 |
| 索引全集成员 `mapping_profile_pkey` 的目录相等比对 | 索引被遗漏或重命名 | 名称不是业务契约；DDL 由真实迁移执行，关键唯一性另以重复写入失败验证，不保留索引目录全集。 |
| 索引全集成员 `mapping_profile_tenant_id_id_key` 的目录相等比对 | 索引被遗漏或重命名 | 名称不是业务契约；DDL 由真实迁移执行，关键唯一性另以重复写入失败验证，不保留索引目录全集。 |
| 索引全集成员 `uq_mapping_profile` 的目录相等比对 | 索引被遗漏或重命名 | 名称不是业务契约；DDL 由真实迁移执行，关键唯一性另以重复写入失败验证，不保留索引目录全集。 |
| 索引全集成员 `mapping_version_pkey` 的目录相等比对 | 索引被遗漏或重命名 | 名称不是业务契约；DDL 由真实迁移执行，关键唯一性另以重复写入失败验证，不保留索引目录全集。 |
| 索引全集成员 `mapping_version_tenant_id_profile_id_id_key` 的目录相等比对 | 索引被遗漏或重命名 | 名称不是业务契约；DDL 由真实迁移执行，关键唯一性另以重复写入失败验证，不保留索引目录全集。 |
| 索引全集成员 `uq_mapping_version` 的目录相等比对 | 索引被遗漏或重命名 | 名称不是业务契约；DDL 由真实迁移执行，关键唯一性另以重复写入失败验证，不保留索引目录全集。 |
| 索引全集成员 `quarantine_record_pkey` 的目录相等比对 | 索引被遗漏或重命名 | 名称不是业务契约；DDL 由真实迁移执行，关键唯一性另以重复写入失败验证，不保留索引目录全集。 |
| 索引全集成员 `idx_quarantine_open` 的目录相等比对 | 索引被遗漏或重命名 | 名称不是业务契约；DDL 由真实迁移执行，关键唯一性另以重复写入失败验证，不保留索引目录全集。 |
| 索引全集成员 `uq_quarantine_raw` 的目录相等比对 | 索引被遗漏或重命名 | 名称不是业务契约；DDL 由真实迁移执行，关键唯一性另以重复写入失败验证，不保留索引目录全集。 |
| 索引全集成员 `listing_pkey` 的目录相等比对 | 索引被遗漏或重命名 | 名称不是业务契约；DDL 由真实迁移执行，关键唯一性另以重复写入失败验证，不保留索引目录全集。 |
| 索引全集成员 `uq_listing_external` 的目录相等比对 | 索引被遗漏或重命名 | 名称不是业务契约；DDL 由真实迁移执行，关键唯一性另以重复写入失败验证，不保留索引目录全集。 |
| 索引全集成员 `idx_listing_spu` 的目录相等比对 | 索引被遗漏或重命名 | 名称不是业务契约；DDL 由真实迁移执行，关键唯一性另以重复写入失败验证，不保留索引目录全集。 |
| 索引全集成员 `idx_listing_shop_status` 的目录相等比对 | 索引被遗漏或重命名 | 名称不是业务契约；DDL 由真实迁移执行，关键唯一性另以重复写入失败验证，不保留索引目录全集。 |
| 索引全集成员 `listing_sku_pkey` 的目录相等比对 | 索引被遗漏或重命名 | 名称不是业务契约；DDL 由真实迁移执行，关键唯一性另以重复写入失败验证，不保留索引目录全集。 |
| 索引全集成员 `uq_listing_sku` 的目录相等比对 | 索引被遗漏或重命名 | 名称不是业务契约；DDL 由真实迁移执行，关键唯一性另以重复写入失败验证，不保留索引目录全集。 |
| 索引全集成员 `sync_job_pkey` 的目录相等比对 | 索引被遗漏或重命名 | 名称不是业务契约；DDL 由真实迁移执行，关键唯一性另以重复写入失败验证，不保留索引目录全集。 |
| 索引全集成员 `uq_sync_job` 的目录相等比对 | 索引被遗漏或重命名 | 名称不是业务契约；DDL 由真实迁移执行，关键唯一性另以重复写入失败验证，不保留索引目录全集。 |
| 索引全集成员 `sync_cursor_pkey` 的目录相等比对 | 索引被遗漏或重命名 | 名称不是业务契约；DDL 由真实迁移执行，关键唯一性另以重复写入失败验证，不保留索引目录全集。 |
| 索引全集成员 `uq_sync_cursor` 的目录相等比对 | 索引被遗漏或重命名 | 名称不是业务契约；DDL 由真实迁移执行，关键唯一性另以重复写入失败验证，不保留索引目录全集。 |
| 索引全集成员 `sync_run_pkey` 的目录相等比对 | 索引被遗漏或重命名 | 名称不是业务契约；DDL 由真实迁移执行，关键唯一性另以重复写入失败验证，不保留索引目录全集。 |
| 索引全集成员 `idx_sync_run` 的目录相等比对 | 索引被遗漏或重命名 | 名称不是业务契约；DDL 由真实迁移执行，关键唯一性另以重复写入失败验证，不保留索引目录全集。 |
| 索引全集成员 `idx_sync_run_running` 的目录相等比对 | 索引被遗漏或重命名 | 名称不是业务契约；DDL 由真实迁移执行，关键唯一性另以重复写入失败验证，不保留索引目录全集。 |
| 索引全集成员 `sync_error_pkey` 的目录相等比对 | 索引被遗漏或重命名 | 名称不是业务契约；DDL 由真实迁移执行，关键唯一性另以重复写入失败验证，不保留索引目录全集。 |
| 索引全集成员 `idx_sync_error_run` 的目录相等比对 | 索引被遗漏或重命名 | 名称不是业务契约；DDL 由真实迁移执行，关键唯一性另以重复写入失败验证，不保留索引目录全集。 |
| 索引全集成员 `dead_letter_pkey` 的目录相等比对 | 索引被遗漏或重命名 | 名称不是业务契约；DDL 由真实迁移执行，关键唯一性另以重复写入失败验证，不保留索引目录全集。 |
| 索引全集成员 `idx_dead_letter` 的目录相等比对 | 索引被遗漏或重命名 | 名称不是业务契约；DDL 由真实迁移执行，关键唯一性另以重复写入失败验证，不保留索引目录全集。 |
| 索引全集成员 `replay_run_pkey` 的目录相等比对 | 索引被遗漏或重命名 | 名称不是业务契约；DDL 由真实迁移执行，关键唯一性另以重复写入失败验证，不保留索引目录全集。 |
| 索引全集成员 `uq_replay_active` 的目录相等比对 | 索引被遗漏或重命名 | 名称不是业务契约；DDL 由真实迁移执行，关键唯一性另以重复写入失败验证，不保留索引目录全集。 |
| 全链最新版本常量 9 与逐级版本 8/7/6/5/4/3/2/1 | 忘记同步版本期望 | 最新版本从嵌入 SQL 文件推导；down 要求版本严格递减且最终为 0、无 dirty |
| 各阶段表数 50、47、39、33、22、17、7 | 某次迁移增删表数量不符 | 这些数字重复表集合；只保留最终回滚到空的零表断言 |
| catalog 7→6→7 额外往返及八张表恢复集合 | catalog down/up 顺序错误 | 一次完整 down 到底、再 up 已覆盖同一 SQL 路径 |
| 000004 down 后七条 shop/credential 外键名称全部消失 | shop 回滚遗留外键 | 最终空空间断言与再次 up 会暴露遗留对象；不在每个中间版本重复目录查询 |
| 000005 down 后 ensure_raw_partition 函数计数为 0 | ingestion 回滚遗留函数 | 全链再 up 重建同名函数会报错；分区函数真实行为仍保留 |
| 000005 down 后三条 mapping 外键名称全部消失 | mapping 外键未回滚 | down 到底和再 up 覆盖对象生命周期，不重复 FK 名称声明 |
| idx_refresh_session_revoked 定义包含 tenant_id/session_id/user_id、logout/kick/replay、WHERE | 索引列序或谓词变化 | 索引文本重复 000003 SQL；会话撤销、登出、强踢、重放行为测试均保留 |
| idx_refresh_session_revoked 在 down→8/7/6/5/4/3 保持文本不变、down→2 消失 | 后续迁移误动会话索引 | 完整 rollback/re-up 验证链；不为非唯一索引逐级重验目录 |
| replay idx_raw_replay_source 列序文本与 down 后索引计数为 0 | 重放索引列或回滚错误 | 重复 000008 SQL；重放扫描、游标、并发、崩溃恢复行为保留 |
| replay_run 九列 authorization_generation/checkpoint_key/function_version/mapping_checksum/mapping_spec/processed/scheduling_generation/total/upper_key 集合及 down 后零列 | 重放迁移遗漏列或未回滚 | 列实际被重放业务测试读取/更新；全链回滚再 up 验证 DDL |
| export_job attempt/row_total/started_at 列集合及 down 后 attempt/row_total 为 0、started_at 为 1 | 导出列增删与版本关系不符 | 真实导出创建/崩溃恢复/行冻结读取这些列，全链再 up 覆盖回滚闭合 |
| export 9→8→9 额外往返及逐阶段 export_job_row 存在性 | 导出行表重复迁移或回滚失败 | 仅保留一次全链往返，export 行复合主键不变量继续验证 |
| 外键目录总数必须等于 20、每条不得位于 sys_refresh_token | 多建或少建外键 | 删除全集约束；物理外键取舍仍由 ADR/迁移 SQL 决定，不额外复制目录 |

保留并迁入结构不变量文件：shop/product_spu version 的 integer/NOT NULL/默认 0；replay_run_item `(run_id,fetched_at,id)` 与 export_job_row `(export_id,attempt,ordinal)` 复合主键。新增真实写入的租户内唯一、幂等键唯一，以及分区跨月/无默认分区拒绝写入验证。

## 实现说明与验收记录

模板摘要同时包含 up/down SQL 的路径及内容。模板由真实迁移回调生成，回调退出前关闭专用连接池；模板不运行 SeedIdentity 等测试种子逻辑，迁移本身创建的默认租户等基础行仍来自原始 SQL。克隆与模板构建使用跨进程 advisory lock；测试清理先关业务连接池，再普通 DROP DATABASE，针对 PostgreSQL 后端退出的 55006 状态限时等待。启动时只清理当前角色拥有、匹配测试前缀、超过一小时且无活动连接的库。

测试运行时额外复用一次生成的随机密码及 bcrypt hash；每个测试仍创建独立用户、数据库和 Redis，真实登录与密码验证照常执行。公共夹具将同时存活的普通测试限制为 3 个；Replay 的短租约、容量和子进程场景留在 Go 原生串行阶段，避免不相关测试争用资源影响原有预算。TestMain 使用 Gin 测试模式，关闭重复路由调试输出，错误及测试日志保留。未调整任何业务行为断言或超时阈值，也不改变 PostgreSQL 同步提交设置。

覆盖核对（比较 8394c68 与本次工作树的 Test 函数名称集合）：

```text
bootstrap source: 186 -> 186
removed: []
added: []
Source 149 = shards 148 + excluded 1
PASS: every integration test has exactly one shard or an explicit exclusion.
migration-related test files: 2
changed Go files max=183 lines, functions max=40 lines
rg -n 'TODO|not implemented' internal/: no matches
product Go/SQL/module dependency changes: 0
```

本地原始基线（真实 PostgreSQL，改动前采集）：

```text
go test -json -count=1 -p 1 ./internal/bootstrap -run Integration
ok github.com/omwk/go-admin-api/internal/bootstrap 266.106s
142 top-level business integration tests passed
```

最终冷启动（先删除同内容摘要的、当前测试角色拥有且无活动连接的模板，再执行；不保留构建缓存）：

```text
go test -v -count=1 -timeout 15m -p 1 ./internal/bootstrap -run Integration
test database template build: commercehub_tpl_84d6a74db0eeeed33f379bba5a3345fd (real migration chain)
build log count: 1
clone log count: 153
PASS
ok github.com/omwk/go-admin-api/internal/bootstrap 128.630s
142 top-level business integration tests passed
266.106s -> 128.630s: -51.66%; <=165s
```

原始角色尚无 CREATEDB 时，已真实执行 schema 回退，不通过模拟权限错误替代：

```text
go test -v -count=1 ./internal/testenv ./internal/kernel/db
WARNING test database: no CREATEDB permission; using schema + real migrations
--- PASS: TestDatabaseIntegrationClonesAreIsolatedAndDropped
ok github.com/omwk/go-admin-api/internal/testenv 2.622s
ok github.com/omwk/go-admin-api/internal/kernel/db 6.144s

go test -json -count=1 -p 1 ./internal/bootstrap -run Integration
ok github.com/omwk/go-admin-api/internal/bootstrap 269.900s
```

同一包只告警一次；隔离测试的两次回退分别执行真实迁移。用户随后授予 commercehub_test CREATEDB。CI 使用管理员另建 NOCREATEDB 角色再次覆盖回退，避免依赖本地角色状态。

运行时与迁移重复验收：

```text
go test -count=20 ./internal/testenv ./internal/kernel/db
ok github.com/omwk/go-admin-api/internal/testenv 32.246s
ok github.com/omwk/go-admin-api/internal/kernel/db 155.093s
```

调度调优期间的失败记录：首次并发 4 个夹具且 verify 同时运行时，重启子进程的连接超时；随后 Replay ETA/Pin 场景触发固定 2 秒租约期限。改为 Go 原生串行阶段后仍复现；仅单独执行 ETA 场景 20 次，S2 工作树 17 次通过、3 次失败，基线 8394c68 为 19 次通过、1 次失败。独立基线检出运行同一命令，确认原业务代码也能复现。运行时/迁移的 count=20 全绿不等于 bootstrap 全量 count=20 全绿，后者尚未达成，未删除、跳过或放宽任何行为断言。曾尝试测试连接 synchronous_commit=off，但没有消除问题，已撤销。

```text
go test -json -count=20 -timeout 15m ./internal/bootstrap -run '^TestReplayIntegrationETAUsesWallClockAndExpires$'
S2 isolated: 17 passed, 3 failed; package FAIL 98.407s
8394c68 baseline: 19 passed, 1 failed; package FAIL 148.015s
error: timeout: context deadline exceeded
```

偏差说明：除模板复用外，还缓存测试密码材料、限制夹具并发并关闭路由调试输出，均属于测试运行时；首次串行克隆实测 196.513s，单靠克隆不能达到本机 165s 门槛。bootstrap 全量重复受上述既有 2 秒租约失败阻碍，已请求人决定是否接受偏差，不能自行视为通过。合并后观察后续 3 个 PR 属于自然观测，交付时无法提前声明完成。

最终完整验证（2026-11-07，同一最终代码）：

```text
make lint
golangci-lint run
exit=0

make test
go test ./...
ok github.com/omwk/go-admin-api/internal/testenv (cached)
ok github.com/omwk/go-admin-api/internal/worker (cached)
exit=0

powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1 -Integration -Migration
== go vet ==
== golangci-lint ==
== go test ==
== integration ==
budgets: db.txn_timeout=15s cache.op_timeout=5s db.migrate_timeout=5m
ok github.com/omwk/go-admin-api/internal/kernel/db 4.869s
ok github.com/omwk/go-admin-api/internal/bootstrap 145.001s
ok github.com/omwk/go-admin-api/cmd/cli 13.653s
== migrate up/down/up ==
version=9 dirty=false
version=8 dirty=false
version=9 dirty=false
all checks passed
exit=0
```

日志检查：新增日志只有模板构建/克隆名称、权限回退警告；连接失败使用脱敏错误，不输出 DSN、密码或种子材料。未新增接口，接口表不适用。Git 差异仅为 internal/testenv 测试运行时、*_test.go、CI 测试执行和两份文档；未改产品 Go、迁移 SQL、go.mod/go.sum。CI 推送后的实际链接与分片耗时随交付回复提供，未提前声明成功。
