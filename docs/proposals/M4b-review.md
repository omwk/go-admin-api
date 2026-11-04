# M4b Gate A 设计评审（完整强度）

日期：2026-11-04。状态：**待人工裁决，不作为已获准的实现规格**。范围：历史重放执行器与异步 CSV 导出；本次只新增本文，不改代码、迁移或已接受 ADR。

依据：`AGENTS.md`、`docs/PROMPTS.md` 模板 1、`docs/04-conventions.md`、`docs/milestones/M4.md`、ADR-016/017；定向核对 PRD 的 REQ-SYNC-002/007/008/011/014、REQ-PRD-004/005/006/008，架构 §2/4.1/7，数据模型的 raw/replay/export/catalog 表，以及平台同步文档 §6/8/9。代码证据均为当前工作区静态阅读，未把方案验收写成已通过的测试。

结论：现有 Apply/Upsert 与页提交机制可以复用，但**释放 Redis 锁不等于公平；上界不等于冻结成员；过滤快照不等于内容快照；来源身份不等于上游业务版本**。以下六问需要补齐执行契约后再进入 M4b 实现。保留 ≤200/批、共锁、旧来源整组跳过、只 upsert、不自动归并等已裁决边界。

## 1. 规格冲突与歧义

| 编号与位置 | 问题 / 两种理解 | 建议与依据 |
|---|---|---|
| C1：ADR-016 §5、ADR-017 §2、M4 §7.6 | “有待跑同步就让出”可以是严格无限优先，也可以是通常优先但有等待上限。持续同步积压时，前者与重放必定前进不能同时成立。 | Q1 明确选择有界优先；这是需人工确认的优先级细化，不声称 ADR 已授予公平份额。若坚持严格优先，必须承认没有无条件重放完成保证。 |
| C2：ADR-017 §2；`sync/orchestrator.go:72`、`bootstrap/sync_runtime.go:84` | M3 Guard 包住最长 60 秒的执行片段，可含多页；不是每 200 条让出。`bootstrap/sync_page.go:25` 又限制每页 ≤100。 | 历史重放独立批次入口 ≤200，正常同步保留 ≤100 页限制；共享解释/写入端口，不能直接把 M3 的页上限放宽到 200。 |
| C3：M4 §7.6、数据模型 replay_run、`000006_sync_init.up.sql:73` | `from_raw_id/to_raw_id` 只有 ID，不能表达时间 keyset、冻结成员、排队代次、恢复检查点。仅保存在内存与持久化到 stats 是两种不同保证。 | Q2 使用 stats 的版本化控制元数据与独立持久成员清单；具体新增结构需批准，不擅改 000006。 |
| C4：`05-platform-sync.md` §8.2、M4 §0、ADR-017 §2 | 旧文档写 raw_id 升序、时间 BETWEEN；新 ADR 写 `(fetched_at,id)`，工程约定含头不含尾。 | 扫描按 `(fetched_at,id)`，时间范围 `[from,to)`；ID 范围沿现有 BETWEEN 解释为含两端 `[from_raw_id,to_raw_id]`，创建时规范化。扫描顺序与来源比较顺序分开，不能把 scan key 直接当 source_order。 |
| C5：ADR-017 §2；`catalog/listing.go:215` | 当前缺失/损坏保留区返回“incoming 更新”，而此前 M4 评审建议缺元数据不可推断；相同 order、不同 fetched_at 当前仍按时间判新旧。 | Q3 用完整 RawRef 校验身份；缺元数据进入不可判定处置，不能默认旧。保留 ID 优先的现有来源序，不暗改为时间优先。 |
| C6：ADR-016 §2/7；`bootstrap/sync_management.go:144` | ADR 允许可重放归档期，但当前 LoadReplay 只查在线 raw；raw_dedup 的存在不证明 payload 可读。 | M4b 首版只接受可证明在线完整、可 pin 的范围；已归档范围明确拒绝。归档恢复后另建 run；在线执行中丢失 raw 则停在原检查点，不能空页成功。 |
| C7：ADR-017 §6、M4 批次表/§7.8；`000007_catalog_init.up.sql:116`、`catalog/model.go` | 任务卡说 000008 建 export_job、进度进 file_object；实际 000007 已建表/model，ADR 规定 file_object 只记完成文件。 | 000008 只能增量补齐获批结构/索引，不能重建表或修改 000007；进度落 export_job。M5 §2 还预占 000008，迁移号需在后续任务卡协调。 |
| C8：ADR-017 §6、REQ-PRD-008、工程约定 §4.3/5、`kernel/db/db.go:113` | 创建时冻结 filters 可能指“按批读取最新值”，也可能指“同一时刻完整内容”。后者若维持长 MVCC 事务会超过 2 秒告警 / 3 秒 DB 预算。 | Q5 推荐 worker 开始时一次物化内容快照，之后短事务读取。快照构建也必须压测并受 3 秒硬预算，不能隐藏长事务例外。 |
| C9：平台同步 §9；`sync/metrics.go`、`bootstrap/sync_metrics.go` | 文档的 lag 是增量水位年龄；现实现是全部资源最老 cursor.updated_at 的年龄，且没有 shop 标签、未排除停用 job。 | Q6 新增真实就绪等待与完整周期新鲜度指标；不能用现有聚合指标断言“重放导致某店延迟”。 |

PRD 的历史重建与 ADR 的来源防回滚应联合解释为“在当前来源允许的范围内重解释”，不是强制让所有目标实体最终使用指定映射。否则 Q4 的新来源保护与 REQ-SYNC-011 会被误读为互相冲突。M4 §7.1 的编码自动挂接等旧段落已由 ADR-017 覆盖，不在本轮重新讨论。

## 2. 实现风险（按严重度排序）

| 级别 | 触发条件 → 影响 | 防护 / 验收位置 |
|---|---|---|
| P0 | Redis 租约过期后旧 worker 恢复；新 worker 已提交 → 旧结果覆盖、检查点倒退。 | 共用 DB 调度代次 + run sequence 双重 fence；所有目标写入者遵守同一 job 行锁顺序，见 Q1/Q2。仅 cancel ctx 或提交前 GET Redis 都不封闭竞态。 |
| P0 | 仅冻结 max tuple；较早分配 ID 的事务晚提交或补写旧 fetched_at → 途中新增成员或遗漏。 | 建 run 同一 MVCC 视图物化成员；每条处置与 checkpoint 原子提交，见 Q2/Q4。 |
| P0 | 缺来源保留区被当作旧；或用新映射优先级越过新 raw → 旧价格/库存整组回滚。 | Q3 判定表、身份校验、不可判定保留记录，父与 SKU 不拆事务。 |
| P0 | 导出分批按 updated_at/价格/title 读活动表；重放移动排序键或改变资格 → 重复、遗漏、混合时刻。 | 一次物化内容及稳定序号，再按序号 keyset；单独冻结 ID 列表也不足，见 Q5。 |
| P1 | 同一 run 重投只靠目标 upsert 幂等 → stats、快照、死信、下批事件重复。 | 检查点序号 CAS、行锁与 outbox 同事务；处置计数守恒，见 Q2。 |
| P1 | 正常同步持续到达、worker 池满载、故障退避也被当“待跑” → 重放永远让出。 | 有界优先、公平债务持久化、独立调度容量；不可运行与可运行分离，见 Q1。 |
| P1 | run 中途分区 detach/drop；归档对象存在但无 manifest/摘要校验 → 少处理却成功。 | 原子在线 pin 与维护互斥；归档缺失 fail closed，见 Q2。 |
| P1 | CSV 上传成功、DB 完成提交失败；重试覆盖同一 key；清理旧 attempt 删除新文件 → 错文件或悬挂对象。 | attempt fence、私有暂存对象、校验后发布、可重试清理，见 Q5。 |
| P1 | 吞吐只看 active processing、统计按 SKU 数估 ETA、cursor 更新时间被误作业务水位 → 六小时任务被预测为几分钟，延迟告警失真。 | raw 处置数/墙钟窗口、等待分解、新鲜度双指标，见 Q6。 |

### Q1 锁让出与公平调度

**“待跑同步”定义**：同一 `(tenant_id,shop_id,resource)`，DB 中 job 仍启用且店铺/授权有效，run 未终结、仍有 pending 工作，当前 dispatch 代次有效，`available_at ≤ DB now()`，且没有有效执行 grant。初始 dispatch 与续跑、到期重试都算；未来 cron、Retry-After 尚未到期、已撤权、终结或过期重复消息不算。单条重同步走同样优先级；M3 隔离/死信重解释属于重放类别，不能伪装为同步抢占。

不能只查队列长度、`status='running'` 或 outbox 的 pending 状态：已投递的消息也可能尚未执行，partial run 可能仍 pending，故障退避不应长期阻塞重放。建议由持久调度意向记录“下一次可运行时间/有效代次”，与对应 outbox 同事务更新；outbox 仅负责唤醒。M4b replay 不登记成占位的 sync_run，否则 M3 的 CreateRun 会把正常同步永久挡在“已有运行任务”检查外。

M3 的 Start 当前先拿 Redis Guard 才建 run，锁忙时连“同步已到达”的持久证据都没有；推荐先在 job 行锁下创建/去重同步 run、登记意向与 outbox，再竞争执行 grant，仍保持同锁域最多一个未完成正常 sync_run。两类任务只在执行时共锁，不把“创建请求必须拿执行锁”当作准入条件；cron 重复触发合并到现有同步意向，不无限堆积过期 run。

**推荐有界优先规则（需 Q1 批准）**：

1. 同一锁域最多一个活动历史 replay；正常同步与其可同时排队。sync 在通常竞争时先获 grant；replay 连续 ready 等待期间，最多允许 **4 个 sync 执行片段** 越过它，第 5 次 grant 必须给 replay。正常同步片段仍 ≤60 秒；replay 每 grant ≤200 raw，整个持锁工作预算 ≤2 秒，超时回滚并减小后续批次。
2. 每次 grant 的选择、计数、owner/代次、截止时间，在 job 对应 DB 调度行锁下原子决定；重排、进程重启不清零公平债务。ready 刚出现时已经持有的 sync 片段计入最多 4 个；不靠随机抢 SET NX 或队列优先级实现计数。只要 replay 仍 ready，连续 sync 的等待债务不能被失败或重复消息重置。
3. 调度交接预算 **≤5 秒/次**，为同步和重放预留可获得的 worker 容量。健康资源条件下，ready replay 首次获得 grant 的上限为 **4 × 60 + 5 × 5 = 265 秒**；持续积压时每至多 5 个有效 grant 至少 1 个属于 replay。这是服务机会保证，不宣称过载时吞吐足以清空全部历史。
4. sync 在 replay 开始后变 ready，重放造成的额外阻塞 **≤2 秒 + 5 秒交接 = 7 秒**；告警用 10 秒阈值留抖动余量。sync 自身排队或平台 API 时间另计。replay grant 后提交不了数据，计作已服务机会但不是进度；连续失败触发 Q6，不无限自称公平完成。
5. 高延迟保护可暂停 replay 新 grant，但必须持久记录 pause 原因/起止时间。265 秒保证只适用于**未暂停且 ready**的区间；暂停超过 15 分钟告警并明确 ETA unknown，不把暂停时间从用户端总耗时抹去。DB/Redis/存储不健康也不能承诺该时限。

若每 15 分钟一次同步且每轮在 60 秒内完成，空档自然足够重放；真正反例是同步需要多次续跑或队列持续非空。有界优先在此仍给 replay 服务机会。严格同步绝对优先在无限积压下不可能同时给有限等待上界，不能用“通常有空档”代替证明。

**锁与 fence**：沿实际 key `lock:sync:{tenant}:{shop}:{resource}`（不是漏 tenant 的示意 key）。Redis 仍做跨进程互斥；DB grant 代次才是提交授权。短事务首先锁调度行、验证 owner/代次/有效期，再锁 run/checkpoint，最后按确定顺序锁 listing/SPU；接管也锁同一行并增加代次，因此失租旧 worker 不能在新代次提交后再写。事务期间接管等待，禁止先写子表再验证 fence。把代次存入已有 job.options 的保留调度区或新类型化调度表见方案 A；不把此事描述成现有锁已提供的能力。

**验收**：可控时钟下让 sync 永远 ready，重排、双 worker、队列乱序并重启调度进程，验证 ≤4 次超车、≤265 秒 grant；replay 持锁后注入 sync，验证额外阻塞 ≤7 秒；未来可用任务不抢优先级；失租旧 worker 在新代次之后零提交。预算含调度/DB 等待；压测不能满足即不接受此公平性声明。

### Q2 检查点与事务边界

**冻结清单而非长事务**：创建 run 时，以一个短事务中的同一 MVCC 视图确定租户/店铺/资源、mapping ID/checksum/函数版本、范围、成员及精确总数。建议新增持久 `replay_run_item`，每项保存 run/tenant、完整 RawRef `(fetched_at,id)`、external_id 与摘要；唯一键含 run 和完整 RawRef，排序索引以 run/tenant 与 `(fetched_at,id)` 为前缀。time/ids/quarantine 三种 scope 都展开成去重后的同一清单，最多读取/物化 500001 项以判超限，超过 500000 则整次回滚并返回既有 60601。

建 run、成员、在线 pin 与首个 outbox 同事务提交；HTTP 201 只在该冻结成功后返回。清单不能靠数小时 READ COMMITTED 分页慢慢补齐。**构建清单同样受现有 3 秒 DB 预算限制，目标 <2 秒**：50 万条压测若超时，创建回滚、没有半冻结的可运行任务；必须先优化索引或另批批准专用快照构建预算，不暗改 kernel/db 全局超时。50 万是安全上限，不是已证实在当前硬件可接受的容量承诺。

**每批语义**：读下一段冻结成员 `last_key < (fetched_at,id) ≤ upper_key`，最多 200；上界取清单最后一个 tuple，不用分别 max(time) 与 max(id) 拼出不存在的 tuple。读取 immutable raw/校验摘要可在写事务外完成，Apply 在受共锁预算约束的批内完成，不调用平台或对象存储；写事务再核对 run 版本/授权代次/清单身份。目标 upsert、库存快照、SPU 重算、隔离/死信、逐 raw 终局处置、stats 增量、checkpoint、下批 outbox **同一事务**。

检查点保存 schema_version、最后已处置 tuple、sequence；队列只携带 run/tenant/dispatch 代次，恢复时重读 DB。检查点不动 sync_cursor。每条必须有持久处置后才能跨过；旧来源 skipped 和同 checksum no-op 也是处置。映射/校验错误可隔离或死信并继续；DB 错误、权限/fence 失效、raw 不可读都不能当单条普通错误吞掉后推进。

| 崩溃点 / 并发 | 恢复结果与不可破坏的不变量 |
|---|---|
| 已读/已映射但未开写事务 | 从原 checkpoint 重读；允许重算，不承诺 CPU 层“只执行一次”。 |
| 第 100 条已 upsert，后续写入或 stats/checkpoint 失败 | 全批回滚，包括 SKU、快照、死信；恢复重做整批，没有半批永久副作用。 |
| DB commit 成功，ack/进程失联 | 重投用旧 sequence 被拒绝；按 DB checkpoint 继续，不能再次加 stats/快照/事件。 |
| COMMIT 回包丢失，结果未知 | 重连读 run sequence/checkpoint 决定是否已完成，不盲目补计；事务失败与提交未知分开处理。 |
| 两个 worker 同 run、或旧租约 worker 复活 | 调度代次 + run 行锁/sequence CAS 只允许一个有效提交；失败者重读，不跳 key。 |
| 有映射坏记录，死信写入也失败 | 整批回滚；没有持久失败处置就不能把该条算 done。 |

“不重不漏”指每个冻结 raw 在该 run 中**恰有一个已提交处置**，不是绝不再次读取/调用纯映射。主计数互斥：`processed = inserted + updated + skipped_stale + skipped_same + quarantined + failed`，`processed ≤ total`；不可判定归入 quarantined 并带原因；SKU 行数和快照行数另计，不能混入 raw 进度。结束需 checkpoint 耗尽清单且 processed=total；无失败/隔离才 success，否则 partial；基础设施重试耗尽为 failed 且明确未处理剩余数。

**跨分区定位**：扫描清单按时间/ID排序，再以完整 RawRef 和 tenant/shop/resource 去分区父表读取；fetched_at 使用 DB 已提交的 UTC 微秒精度，不重建为当前时间/毫秒。月边界由 fetched_at 范围分区裁剪，id 只作同时间 tie-breaker；ID scope 先从受限 raw_dedup/在线表解析复合引用，缺失或多义的 ID 不猜测。现有 raw_lookup 并非此扫描索引，需要冻结/清单 EXPLAIN 与跨月 fixture 验证；不能假设所有月份每批全扫可接受。

**归档边界**：推荐 M4b 仅在线重放。建 run 与维护 detach/drop 必须遵守同一个维护锁/持久 pin 协议：活跃 run 所需月份不可移除，pin 保留至终态和恢复窗口；不是用一个很短 TTL 让六小时任务悄悄失去保护。创建时缺分区/成员不可读：已知归档范围用 60601，明确不存在的单条 raw 用 60501；临时 DB/存储不可用是基础设施失败，不能伪装成空范围。

运行中发现已 pin raw 缺失、摘要不符或分区脱离，暂停推进，持久 error_reason 并告警；重试 1/5/15 分钟后仍不可读则 failed（checkpoint 保留，支持修复后按同一清单恢复），不计 skipped、不降低 total。恢复归档应在独立维护流程校验 manifest 的对象版本、范围、条数、摘要、读回并恢复原 ID/fetched_at；若未来直接读归档，先完成可校验 staging 再进短写事务，不能在事务里下载。当前目录仅有 RawRef，不等于已有归档回读实现。

### Q3 来源仲裁的边界

**两种顺序**：扫描序 `K=(fetched_at,id)`；现实现来源主序 `O=source_order=raw.id`，证据为 `sync/interpret.go:16` 与 `catalog/listing_test.go:25`（较低 ID 即便 fetched_at 晚一小时也被拒绝）。同一 raw 身份是完整 `(id,fetched_at)` 且有匹配目录/tenant/shop/resource/external_id；checksum 只决定是否重解释，绝不决定来源年龄。保持 ID 主序意味着保证的是“系统已接受的来源序不倒退”，不是上游商品业务更新时间的绝对最新。

| 输入与当前父 listing 的关系 | 推荐判定 | 父 / 全部 SKU / stats |
|---|---|---|
| 父不存在，来源完整可信 | 插入，任一已发布目标映射均须匹配平台/资源 | 父和映射得到的全部 SKU 同事务；inserted+1。 |
| incoming O < current O，无论时间/映射版本 | 旧来源，整组跳过 | 不改父、SKU、库存快照、SPU 汇总；skipped_stale+1。 |
| incoming O > current O，身份可信 | 新来源，允许应用 | 同事务更新父与本次完整映射子集合；updated+1。 |
| 同一毫秒（甚至同一微秒），ID 不同 | 仍按 O 比较；不按时间相等判同源 | 高 ID 可更新，低 ID 整组跳过。 |
| 完整 RawRef 相同、checksum 相同 | 幂等 no-op | 无重复快照/副作用；skipped_same+1。 |
| 完整 RawRef 相同、checksum 不同 | 允许重解释，与版本号大小无关 | 同事务更新父与全部输入 SKU；updated+1。 |
| O 相同但 fetched_at 不同、raw_id 与 source_order 矛盾、目录多义 | 身份不一致，不能用“较晚时间”自动放行 | 零目标修改，quarantined+1，原因 source_identity_ambiguous。 |
| 父存在但无保留区、缺字段、时间非法、整数溢出/失真或版本不认识 | 不可判定，不能设 O=0 | 尝试从现有 listing.raw_id 与唯一可信目录/应用记录恢复；不能证明则零目标修改，quarantined+1，原因 source_metadata_unknown。 |
| 同商品跨月，正常全局 raw ID 且不同 RawRef | 分区不是来源序命名空间 | 比较全局 O，不重置顺序；旧月份高 ID 仍按现有 ID 主序处理，记录时间倒置异常。 |

不可判定处置必须持久化到该 run 的成员处置与诊断记录（安全引用/原因，不放原文）；若复用 quarantine_record，要先批准扩展原因白名单，不能伪装成“映射错误”。metadata 恢复只修来源证据，不按当前重放 raw 猜当前来源；普通同步遇到同样未知行也不能因“sync 优先”无条件初始化。需要人工确认来源后修复再建 run；该动作应有独立授权/审计，本次评审不实现修复 API。

保留区只能引擎写，读时用精确 int64/typed JSON，不能将大 ID 经过 float64（当前 `json.Unmarshal` 到 map 的路径需覆盖 >2^53 测试）。SKU 不加独立来源元数据；父通过仲裁后才处理子集合，保留人工 spu_id/sku_id 绑定，不自动归并；“完整子集合同事务”不意味着删除本次未出现的历史 SKU，重放仍只 upsert。

**当前能力的明确边界**：raw_dedup 按内容返回原始 RawRef，A→B→A 的第三次观测可能复用第一次较低 ID，被当旧来源；ID 分配顺序也不等于事务提交顺序，上游晚到旧事件可获得高 ID。现模型没有独立观测序或可信上游版本，无法同时证明“最新真实观测绝不丢”和“历史来源绝不回滚”。不在 M4b 偷改去重/来源语义；如业务要求最新观测保证，需另行批准 observation/upstream-version 设计。Q4 的五次更新结论以其已被来源仲裁接受为前提。

**验收矩阵**：上表每行至少一个用例；额外覆盖缺元数据不能改 SKU、checksum 新但 O 旧、相同时间两个 ID、同 ID 异分区引用冲突、大整数、跨月排序与仲裁顺序相反、两个 listing 并发影响同 SPU。检查父/全部 SKU/快照/统计一致，不能只断言父标题。

### Q4 重放范围冻结

冻结的是**集合 + 解释器输入 + 身份**：创建时可见成员、精确总数、上下界、scope 原始条件及规范化条件、tenant/platform/shop/resource、mapping_version_id/checksum/函数集版本、请求人；quarantine scope 冻结 raw 成员而不是执行时再查 pending。ID 输入去重、权限验证、映射归属验证也在此时完成；空集合直接形成 total=0 的终态成功，不启动空循环。

冻结后新 raw（包括时间戳恰好小于上界的延迟提交）**均不加入该 run**；由正常同步处理，或另建新的 replay。active mapping 切换不改变既有 run；checksum/函数版本无法重载一致时停，不使用新 active 静默替代。冻结租户不冻结权限：每批重验请求人的权限、shop 所属及授权代次，撤权后停；不能拿创建快照保留已撤销权力。

50 万条跑 6 小时示例：创建时清单含 R0，目标 checksum=C1；随后同步依次接受 R1…R5（均比 R0 更新，最后一次使用正常同步固定版本 Csync）。若 replay 先写 R0，则后续同步覆盖为 R5；若 replay 在任意 Ri 之后遇到 R0，则整组跳过。因此最终该 listing 与其输入 SKU 保持 **R5 + Csync**，不会被 R0+C1 回滚；若 R5 恰为同一 raw 且 checksum 不同，则按 Q3 同源重解释规则，最后合法提交的 checksum 生效，不能偷换成“映射版本号最大者赢”。

历史 run 并不保证全库最终都是 C1，也不保证历史缺失 SKU 被删除。若希望最新来源也用 C1，应先发布/切换正常同步映射，再对当时最新来源另建 run；不能让旧 run 扩 scope。并发新来源被隔离而未成功应用时，当前目标仍是最后成功接受的来源，不凭 fetched_at 假装成功更新。

任务前后 count/checksum 快照可保留作观测，但六小时内正常同步也在变，差值**不是重放因果效果**。统计以 run 成员处置和实际接受写入为准；摘要采用字段版本固定的有序内容摘要，记录采样时间与并发说明，不能把数据库 sum/hash 的差值称为“修正了多少商品”。

**验收**：创建视图之后提交一个 tuple 小于上界的 raw，确认旧 run 不处理；发布新 active 后旧 run checksum 不变；quarantine 状态改变不改变清单；对同商品交错 R0 重放与 R1…R5 同步，最终来源始终不倒退，processed/total 精确守恒；撤权后下一批零目标写入。

### Q5 导出任务

**推荐内容时点**：创建时只冻结 filters/sort/字段版本/tenant/requested_by 及授权边界；实际 CSV 反映 worker 开始构建内容快照的数据库一致视图 **T_export**，不是请求时间、下载时间或逐批读取时刻。任务应暴露 snapshot_at 与 attempt；如果用户必须“点击时刻”的数据，应选择方案 D-C 的额外历史存储设计，当前可变表不能事后还原该时刻。

1. worker 首先重验权限并 claim job/attempt。在一个无外部 IO 的短事务中，把所有匹配商品的**完整 CSV 投影值、冻结排序键和稳定序号**写入 `export_job_row` 暂存表，再原子标记 snapshot_ready；所有关联价格/店铺名等投影同一 MVCC statement 获取，排序加唯一 ID tie-breaker，NULL/字符排序规则和字段版本固定。只物化 ID、不物化值仍会混入重放新价格。
2. 暂存就绪后每批 **≤1000 行**，按 `(export_id,attempt,ordinal)` keyset、`ordinal > last_ordinal` 读取，流式编码/上传；row_count 为实际已输出行数，不是活动表反复 COUNT。重放发生在 T_export 后时，CSV 仍保留 T_export 的值；发生在此前则反映此前已提交结果，绝无一半新一半旧。原请求 sort 决定物化序号，不能为省事改成 ID 排序。
3. 快照构建目标 <2 秒、硬上限 3 秒；10 万行夹带重放/同步负载时须验证。超时回滚、任务失败并有安全原因；不以长事务持有数分钟的 snapshot 来满足 10 万行 <3 分钟。重试若已 snapshot_ready，必须复用同一投影；未 ready 的失败无可交付快照，新 attempt 的 T_export 必须明示，不能称复现了旧时刻。
4. 默认全局 **2 个**运行导出、每租户 **1 个**、每请求人 **1 个**；运行包含构建/上传/发布，不仅写 CSV。DB 原子 claim 及租约/attempt 防双领；pending 每租户最多 **10 个**，第 11 个创建请求返回既有 429/10005，超过 10 分钟排队告警。运行硬预算 **5 分钟**，10 万行目标仍 <180 秒；计时包含快照/编码/上传/发布，不把排队混成执行性能。
5. 上传使用私有 attempt 暂存 key，完成 CSV 后校验行数/字节数/摘要，再发布到已裁决的 `exports/{tenant}/{export_id}.csv`。写 canonical key 的上传/复制可能在租约过期后迟到，DB fence 本身不能阻止存储 IO：发布必须使用存储支持的条件创建/不可变对象版本并绑定 file_object 的确切版本；旧 attempt 的 late write 不得覆盖当前版本。存储端口若不支持该保证，任务不得启用透明并行接管发布，需先确认旧上传结束/abort 后重试，不能假定 S3 copy 与 DB 是一个事务。
6. 对象确认成功后在短事务内写 file_object 与 export_job succeeded/file_id/row_count/finished_at/expires_at；只有该事务提交后才提供下载。DB 提交失败后的私有对象是待协调孤儿，重试校验后登记或清理；不能让 succeeded 先于对象完成。每批授权复核，撤权停止并清理；下载还需校验当前 tenant/owner/权限，不因冻结过过滤条件而越权。
7. 对象从完成时保留 **24h**，签名 URL 有效期取 **min(1h,剩余对象寿命)**；到期不再签名并标 expired。失败立即 abort multipart、删除本 attempt 暂存和无引用发布版本；只删自己确切的 key/version，不能删其他 attempt 成果。失败清理义务与状态同事务进 outbox，网络故障按 1/5/15 分钟重试，持续失败报警；另有每 5 分钟清道夫/存储生命周期兜底，逻辑过期与实际物理清理分别观测。
8. 冻结暂存行成功后保留到该任务终态后的恢复窗口，最长至 expires_at，失败也执行清理；multipart/orphan 清理有独立保留上限。CSV 字段白名单遵循成本价不对外口径，文本处理公式注入（以 ASCII `=`、`+`、`-`、`@` 或制表/回车/换行等开头的文本单元格加前导单引号，再做标准 CSV 引号转义；数值列保持严格数值类型），禁止直接导出任意 ext/保留区；策略随字段版本冻结。

**验收**：导出构建前后注入同商品改价/改 title/改筛选资格/删除，文件逐行等于 T_export 投影且无重复/遗漏；超过 1000 行跨批验证排序、NULL、中文和 CSV 引号/换行；10 万行 <180 秒实测并记录快照时间；双 worker/restart/late upload/DB 完成失败/multipart abort 失败均不可提前下载、不得删新成果；23h59m 下载 URL ≤1min，24h 后拒签，清理重试可观测；并发 claim 与租户隔离矩阵必须跑。

### Q6 观测与具体阈值

以下为**建议上线初值，待 Q6 裁决**，不是现有实现已提供的指标。任务 API 显示 run/job 明细；Prometheus 不加 raw_id、external_id、run_id、URL 等高基数/敏感标签。店铺级诊断从受权限保护的任务记录读取，指标按有限的 resource/class/outcome 聚合，必要时按受控租户维度部署。

**进度与 ETA**：分母是冻结去重 raw 数 total，分子是已提交 processed；展示 updated/inserted/stale/same/quarantined/failed 与未处理数，不能把 skipped 从进度中扣掉。用最近 **15 分钟墙钟**的提交数计算有效速率（包括让锁/重排/重试时间），再用 α=0.3 的 EWMA 平滑，ETA=(total−processed)/rate。至少 3 个已提交批次且覆盖 60 秒后才显示估计；较长窗口作保守区间，UI 标估算与更新时间，不承诺置信概率。500000/6h≈23.15 raw/s 是该例的全程平均，不能拿一次 200 条的 SQL 耗时外推。

暂停、故障、最近 5 分钟无提交或样本不足时 ETA=unknown，并显示 wait_reason/last_committed_at；只在 active 时间统计的吞吐作为另一个诊断值。进度由 DB 重新聚合/读取，重复队列投递不增加 counter；历史 run 清理也不能让指标被误解为单调 counter。导出分开显示快照构建阶段、已输出行数和上传字节，不用 object size 猜行数。

| 指标 / 定义 | 初值阈值、持续时间与动作 |
|---|---|
| replay_ready_wait_seconds：未暂停且 ready 的最老等待 | >240s 持续 1min warning；>265s 持续 30s critical，检查调度份额；sync 越过 replay 超过 4 grants 立即 critical。 |
| replay_last_commit_age_seconds：健康且 ready 的任务距最后提交 | >300s 持续 1min warning；>900s 持续 1min critical。主动暂停单独告警，不伪装无进展。 |
| replay_batch_duration_seconds / replay_batch_raw_count | 整批持锁 >2s 的任何完成/超时记录 warning；10min 内 ≥3 次则暂停该 replay、缩批/排查；count>200 立即 critical。SQL 长事务 >2s 沿约定报警。 |
| sync_ready_wait_seconds：到 available_at 后距获得 grant | >60s 持续 2min warning；>120s 持续 2min critical。包括 outbox/worker 排队，不从“拿到锁之后”才计时。 |
| sync_replay_block_seconds：sync ready 时实际等 replay grant 的时间 | 单次 >10s warning；5min 内 ≥3 次或单次 >30s critical，停止新 replay grant，通知并记录 pause；正常恢复需连续 10min 低于 warning。 |
| sync_cycle_freshness_seconds：启用 job 距最后完整成功周期；另监控 due 周期年龄 | 15min 调度场景 >1200s 持续 5min warning，>1800s 持续 5min critical；其他周期 P 用 P+300/P+900。从未成功的 job 以首个应跑时刻起算；不要用每页 updated_at 掩盖迟迟无法完成的周期。 |
| sync_source_watermark_lag_seconds（仅 timestamp 模式可信源水位） | 保留原规格 >3600s 持续 10min critical；游标 token 模式显示不适用，使用周期新鲜度，不从 token 猜时间。 |
| sync_complete_duration_seconds 与对照基线 | 同平台/资源同负载的关闭重放基线取最近健康 24h P95；开启重放后 15min 窗口 P95 >max(基线×1.5,基线+30s) 且至少 20 个样本持续 10min warning。样本不足依赖上述绝对阈值，不凭相关性断言因果。 |
| replay_fence_reject_total / lease_lost_total / source_ambiguous_total | 5min 内失租或 fence 拒绝 ≥3 warning；失效代次成功写入、checkpoint 倒退、processed>total 或终态计数不守恒任一次 critical。不可判定来源任一次 warning，10min ≥10 critical，给安全诊断引用。 |
| replay_raw_unreadable_total / replay_pin_missing / archived_manifest_mismatch | pin 失效、清单 raw 缺失或摘要错误任一次 critical，停该 run；暂时读取错误按 Q2 重试，不算 done。 |
| replay_paused_seconds / infrastructure_retry_count | pause>900s 持续 1min warning；同批连续 3 次基础设施失败 critical 并进入失败/人工恢复流程，避免无限重试隐藏饥饿。 |
| export_execution_seconds / queue_wait_seconds / active_jobs | 10 万行执行 >180s warning，>300s failed；queue>600s 持续 2min warning；全局>2/租户>1/请求人>1任一次 critical。 |
| export_cleanup_pending_age_seconds / expired_object_count | 失败清理待办>300s warning，>900s critical；超过 expires_at 仍物理存在 >15min critical；每5min核对 manifest/DB 与对象版本，不只看 job.status。 |
| batch_disposition_errors / sync consecutive failed | 10min、processed≥100 时隔离+失败占比>1% warning、>5% critical；同步连续3次 failed 保留现有 critical，重放旧来源跳过率不当错误率。 |

告警验收使用假时钟和持久样本推进跨阈值/恢复窗口，检查暂停原因、去重和恢复；混合负载压测分别记录关闭/开启 replay 的 ready wait、完整周期耗时、源水位/新鲜度，不能只凭现有 `sync_cursor_lag_seconds{resource}` 验收。

## 3. 方案对比

以下四个真实分叉覆盖六问；工期为新增工作粗估，交叉部分不能简单相加，不替代 M4b 9–13 人日的排期裁决。

### A. 公平与提交授权（Q1）

| 方案 | 做法 / 优点 / 缺点 | 表、接口、文件影响 | 工作量 / 推荐 |
|---|---|---|---|
| A-A 严格同步绝对优先 | 有 ready sync 永不发 replay grant；规则简单、无优先级例外；持续积压时 replay 无有限等待/完成保证，只能拒绝准入或等待人工维护窗口。 | bootstrap 同步调度与 replay.stats、任务状态/监控；不需公平计数，但仍需 DB fence。 | 1–2 人日；仅在接受饥饿且不要求本题公平上界时可选。 |
| A-B 有界同步优先 + durable grant | 通常 sync 优先，最多4次超车；重启保留债务，能验证265s；增加调度协调，必须为两类任务保留容量。 | `sync_job.options` 保留调度区（ready_at/owner/epoch/deadline/公平计数），`replay_run.stats`；bootstrap/sync_run、runtime、page、events 与新 replay worker/端口/能力注册及测试；不改对外路径。 | 2–4 人日；**推荐**，优先级细化需人工批准，不能只加一段 Redis 轮询。 |
| A-C 类型化调度表 | 与 A-B 相同规则，将 grant/意向/索引独立类型化；查询/约束清晰；新增表/维护面。 | 新增迁移调度表、scope registry、所有写路径适配；避免高频改 options 的耦合。 | 3–5 人日；负载/治理要求证明 options 不合适时选，不与 A-B 同时实施。 |

### B. 冻结集合、检查点与归档（Q2/Q4）

| 方案 | 做法 / 优点 / 缺点 | 表、接口、文件影响 | 工作量 / 推荐 |
|---|---|---|---|
| B-A 只存上下界 + keyset | 不增成员表、成本小；无法排除晚提交/补写到界内，也不能固定 quarantine 集合/精确分母，严格冻结验收不成立。 | replay_run.stats、在线 raw 查询；若采用需明确降低“冻结”保证。 | 1–2 人日；不推荐用于当前冻结要求。 |
| B-B 持久成员 + 在线 pin | 同一 MVCC 视图物化 RawRef，批次同事务处置；不持六小时事务，精确 total/恢复；成员写放大、创建性能与维护 pin 必须验证。归档范围先拒绝。 | 新 `replay_run_item`/pin 结构及索引、replay.stats 协议；ingestion raw/维护端口、bootstrap 适配器/能力、replay API；000008或后续新增迁移，集成迁移期望仅增补。 | 3–5 人日；**推荐**，50万冻结3s容量是发布门禁。 |
| B-C 成员清单 + 归档回读 | B-B 加 manifest 校验、对象范围读取/staging，可跨完整归档期；满足更广历史范围，但对象故障、清理/版本管理复杂，超出当前已证实能力。 | ingestion 归档存储端口/实现、manifest 与 staging/pin、维护操作；仍不得在业务事务内读存储。 | B-B另加2–4人日；归档范围首批必需时再批准，不假装已包含在9–13人日。 |

### C. 来源不足与仲裁顺序（Q3/Q4）

| 方案 | 做法 / 优点 / 缺点 | 表、接口、文件影响 | 工作量 / 推荐 |
|---|---|---|---|
| C-A 保持当前宽松缺省 | 缺保留区就覆盖、同order异时间继续比时间；兼容老数据处理率高；无法证明防回滚与同源身份。 | 沿用 catalog/listing.go；无需迁移，但不满足安全红线。 | <1人日；不推荐。 |
| C-B ID主序 + 完整身份 + 不可判定处置 | 保留已运行来源语义；遇证据不足先保护现值，raw可重试；老行可能需人工修复，不能保证上游真实观测最新。 | catalog/listing.go 与测试、共享验证端口；run成员诊断/隔离原因白名单、能力/安全展示；保留区仍引擎所有，不给SKU加元数据。 | 1–2人日（独立修复工具另算）；**推荐**。 |
| C-C 改为业务版本/独立观测序 | 能处理A→B→A与可信上游版本，语义更贴近最新观测；需重定义去重与来源证据，旧数据回填/平台版本可信度成本大。 | raw观测关系、source元数据、正常同步与replay全写路径、数据模型/新ADR；不是把比较函数交换两行。 | 另行3–6人日起；当前不采纳，须另卡裁决。 |

### D. 导出内容时点（Q5，观测按 Q6）

| 方案 | 做法 / 优点 / 缺点 | 表、接口、文件影响 | 工作量 / 推荐 |
|---|---|---|---|
| D-A 冻结过滤、按批实时读 | 1000行短查询直写文件，资源小；可变排序键会漏/重，稳定ID也只能消除部分重复，内容仍混合时刻。若选必须声明弱一致，不能验收为快照导出。 | 既有export_job + 新worker/存储端口；API标一致性口径。 | 2–3人日；只有业务明确接受弱一致才可选。 |
| D-B worker时刻内容物化 | T_export一次物化完整投影，短事务keyset上传，重试可复用；多暂存表/容量，快照构建3s需实测。 | 增量扩export_job的snapshot_at/attempt/租约等获批字段、新export_job_row；API展示时点/阶段，worker/存储发布/清理/指标及测试。file_object仅登记完成文件。 | 3–5人日；**推荐**。 |
| D-C 请求时刻/长MVCC快照 | 可从请求视图导出，但长事务维持数分钟违反预算并拖累vacuum；若要异步重启可复现请求时点，需要请求时物化或历史版本存储，额外写放大。 | 专用历史/快照结构和性能预算、额外事务策略；export_job/接口需标时点，不是仅存snapshot token即可重启。 | 4–7人日起；首版不推荐持长事务。 |

所有新增表/列、隔离原因、任务响应字段、调度语义及 timeout 例外都必须先裁决并落正式任务卡/ADR；本文不构成自行扩 schema 或改变 M3 编排的授权。000008 现无文件，000007 已有 export_job，实施前应统一迁移编号与 M5 占号，不改已合并迁移。若采纳 B-B/D-B，须同时批准更新维护 pin 路径、bootstrap 能力/注册与迁移集成期望；不能靠原任务 prompt 的“禁止改编排”实现这些保证。

## 4. 未来 2 个里程碑的坑（只列不展开）

- M5：订单状态机重放不能复用商品“同源换checksum全量重解释”而绕过非法状态流转校验。
- M5：Webhook/轮询的事件版本、晚到事件与A→B→A需要独立观测语义，raw内容去重不能兼任事件新旧判断。
- M5：订单导出的PII二次授权、字段白名单与对象版本下载不能沿用商品默认权限。
- M5：订单迁移000008与M4b增量迁移冲突，必须在开工任务卡重新分配。
- M6：导出快照不等于报价双冻结，商品重放不得改已发share_snapshot或客户专属价格。
- M6：人工归并/解绑与重放同时修改SPU汇总需统一锁顺序，防止串价或死锁。
- M6：一小时预签名URL不能承诺即时撤权，若分享要求即时撤销需有不同下载授权路径。

## 5. 待决策清单

1. **Q1**：是否选 A-B，批准“通常同步优先、最多4次超车、健康未暂停时265秒内给replay grant、sync额外阻塞≤7秒”的持久调度与全写路径fence，并接受高延迟保护暂停会暂时取消等待上界？
2. **Q2**：是否选 B-B，批准新增冻结成员/在线pin、stats检查点协议和同事务处置，首版拒绝已归档范围，且50万成员冻结必须在既有3秒预算内通过压测，否则回滚创建并另议预算？
3. **Q3**：是否选 C-B，保留raw ID来源主序，完整RawRef验证同源，缺失/矛盾证据整组不写并持久隔离，接受其仅保证来源序不倒退、A→B→A最新观测保证另卡解决？
4. **Q4**：是否确认冻结后新raw一律另run处理，历史run不追赶新来源，五次已接受同步后的终态保留R5及其正常映射，before/after摘要仅作观测而非重放因果统计？
5. **Q5**：是否选 D-B，批准worker开始时T_export内容物化及相应增量表/响应字段、全局2/租户1/请求人1与排队10个的限制、对象24h/URL≤1h及具版本保护的发布与失败清理协议？
6. **Q6**：是否接受本评审指标/阈值（ready wait 60/120秒、完整周期新鲜度20/30分钟、重放阻塞10/30秒等），以混合负载和故障注入验证后作为M4b发布门禁，并将新增scope/能力/维护适配纳入正式实施范围及重估9–13人日？

本次交付核验范围：仅本文新增；五节格式、六问、四组方案、判定表、故障恢复/并发验收、阈值与待决策项逐项检查。未运行 make lint/test、迁移或负载测试：没有代码/迁移变更，也未声称任何提案性能、时限或故障演练已经通过。
