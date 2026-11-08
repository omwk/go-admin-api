# M7 · 凭据轮换设计评审（Gate A）

- 日期：2026-11-09；代码基线：`3871bca`；状态：**待人裁决，尚未批准实施**。
- 范围：REQ-SHP-008/009、REQ-SYS-007 中服务于凭据轮换的部分；本次仅新增本文件。
- 结论：原卡的 2–3 人日不足以覆盖一次性 token、故障恢复和进程装配；建议裁决 Q1–Q6 后修订实现卡，推荐组合约 **18–28 人日**，估算口径见 §3.7。
- 既定约束：沿用 ADR-005/015 的版本化 AEAD、五维 AAD、只写不读、90 天密文窗口；沿用 ADR-016 的 WorkAccess 授权原则与模块端口；ADR-002/018/019 的业务 raw、来源仲裁、重放及订单检查点规则不变。本评审不重新选择这些机制。

## 1. 规格冲突与歧义

审阅输入：`AGENTS.md`、`docs/04-conventions.md`、`docs/PROMPTS.md` 模板 1、`docs/milestones/M7.md`、`docs/03-data-model.md` §3/4（并核对 notification、sync_job/run）、`docs/05-platform-sync.md` §2/3、PRD EPIC-2 与 REQ-SYS-007、架构 §2/4.2、ADR-002/005/015/016/018/019，以及下述 M2/M3/S3 实现。额外读取 ADR-015 仅为确认 M2 已裁决的 keyring/AAD 口径，不重开加密评审。

| 编号 | 位置 | 问题与两种理解 | 建议 |
|---|---|---|---|
| C01 | M7 §5.4；数据模型 §3 credential_refresh_run | 只有 running/success/failed/skipped：A 把“已发送、结果未知”也算普通失败重试；B 单独记录发送阶段与结果确定性。一次性 refresh_token 下 A 会再次消费旧值。 | 选 B；保留凭据状态枚举，扩充刷新运行记录的内部阶段，见 Q1。 |
| C02 | `docs/05-platform-sync.md` §2/3；`internal/modules/connector/types.go`、`errs.go` | 文档称已有 Refresh/Check/刷新能力和细分错误；实际 Platform 仅 Code/Capabilities/Fetch，Capabilities 仅资源与分页，错误只有四类。A 直接调用文档接口；B 先批准刷新端口与错误结果的增量契约。 | 选 B；这是待实现的契约缺口，不能假定已有 Refresh，也不能只加 shop/refresh 文件。 |
| C03 | PRD REQ-SHP-008；M7 §5.3/§8；平台同步规格 §3.1 | A 任意三次刷新失败均需重新授权；B 只有授权确实失效才进入 needs_reauth，临时失败走退避/告警。原卡第三次即终止，第四、五档退避不可达。 | 推荐 B，**改变现行 PRD 验收口径，必须由人批准**；首次确定撤销授权也不应盲等三次。 |
| C04 | M7 §5.1/5.4；`internal/bootstrap/shop_credentials.go` 的 WorkerCredential | A expiring 仍可同步且成功后按有效期投影状态；B 只允许 active，并把每次刷新成功一律写 active。当前查询仅接受 active，短 TTL token 按“临期三天”定义将长期处于 expiring。 | 选 A；将“生命周期展示”与“刷新执行阶段”分开，修正使用端口谓词。 |
| C05 | M7 §5.2；数据模型 §3 expires_at/refresh_at、§4 raw_payload | A access_token 过期就不可刷新，且刷新响应照业务 raw 原样入库；B access 与 refresh/授权期限独立，刷新响应属于凭据控制面密文材料。§4 是采集与映射，凭据表实际在 §3。 | 选 B；不得把 token 响应送进 raw/映射/重放。过期 access 仍可刷新，须由平台 refresh 授权期限判定。 |
| C06 | M7 §5.2/5.3；数据模型 §3 refresh_at 与 run.next_retry_at | A refresh_at 已减 lead_time，再用 now+5min 扫描；B refresh_at 是真正到期执行时刻，扫描 now，重试受 next_retry_at 约束。A 可能把“提前五分钟”变成提前十分钟，并每分钟绕过退避。 | 选 B；首次计划与重试资格分开，持久记录下一次允许执行时间并建立相应索引。 |
| C07 | M7 §5.4 的 platform.settings.default_ttl；`migrations/000004_shop_init.up.sql` 的 platform | A 平台不返期限就猜默认 TTL；B 必须有该协议版本的已核验 TTL 规则。实际 platform 无 settings 列，Credential DTO 也未携带期限。 | 选 B；期限策略放批准的适配器配置/能力契约；缺证据不得伪造长期有效期。 |
| C08 | M7 §5.3/§10 禁止修改同步编排；ADR-016 第 9 条；`internal/modules/sync/orchestrator.go` deferFetch、`internal/bootstrap/sync_run.go` DeferRun | A 完全不动同步，过期即 enabled=false，恢复时全部启用；B 增加凭据可用性阻塞/恢复接线，保留管理员开关。现有停用无原因和恢复归属，且当作终态失败。 | 选 B；须批准 Q5 所列的最小同步接线白名单，不能在实现时越过原卡边界。 |
| C09 | M7 §5.5/5.6；PRD REQ-SHP-009/SYS-007；notification 表 | A 每天 09:00+日期键即满足预警/24h 去重；B 持续监测、分 3d/1d/expired 档、滚动 24h 去重。当前唯一键仅 tenant+dedup_key，未包含渠道/收件人，也没有 read_at。 | 选 B；三渠道不能互相去重掉；sent 与用户已读分开，见 Q6。 |
| C10 | M7 §2/T7.1；数据模型 §11；ADR-018 第 7 条；迁移目录 | A 使用卡中固定 000010 或模型建议 000012；B 遵守当前最大号+1。基线最大号为 000014，旧号已有订单等迁移。 | 选 B；当前应从 000015 开始，实施时再核对最大号；仅新增迁移，不重排旧迁移。 |
| C11 | M7 §6/T7.7“手动立即生效”；ADR-015 幂等；架构 §2 事务规则 | A HTTP 等待已持久发布再返回200，执行仍按短事务分段；B 短事务创建持久 run，HTTP 202 返回安全 run_id，worker 执行。两者都不能用跨 HTTP 的 SQL 事务保证原子性，现卡未明确客户端超时后的回执与查询。 | 推荐 B；202/结果查询语义及超时行为需一并批准；若保留 A 也必须共用持久轮换服务，不将“已接收”描述为“已刷新”。 |
| C12 | M7 §5.2；`internal/worker/queue.go`、`internal/bootstrap/worker.go`；S3 setup/steps | A 注册 refresh 队列就代表刷新已装配；B 必须贯通扫描、持久任务、队列类型/路由、处理器、结果与恢复。当前仅注册 outbox:deliver、sync:job，非 sync 事件默认进 notify，S3 用静态 api_key。 | 选 B；S3 的队列存活证据不是刷新链路证据，需新增刷新场景验收。 |

## 2. 实现风险（按严重度排序）

| 严重度 | 触发条件 | 影响 | 规避与验收重点 |
|---|---|---|---|
| P0 | 平台已轮换，响应丢失或所有本地持久化失败 | 旧 refresh_token 已消费，新值丢失；无限重试不能修复 | 结果未知单独建模；只在有平台保证时重试旧值，否则对账或重新授权；注入“成功响应后 DB 失败+进程死亡”。 |
| P0 | 锁 TTL 到期/Redis 丢锁、手动更新撞自动刷新、旧 worker 迟到 | 双消费，或迟到结果覆盖人工新凭据 | 持久发送屏障+版本/代次条件提交；dispatching 未决不因租约过期重新发；HTTP 客户端禁止隐式重试。 |
| P0 | 只验请求 ID，未重验 tenant/shop/credential/授权代次 | 跨租户解密、撤权后继续刷新或写回 | 使用目的限定的工作能力；发送前重验，发布前再验；撤权后只允许受控保存已返回密文、不得激活。 |
| P0 | 直接把刷新响应写 raw、run.error_msg、日志、Webhook 或队列 | token、签名密钥、PII 泄露 | 密文暂存走既有 Sealer；固定元数据白名单；六通道泄露断言；平台错误 Cause 也不得保留敏感报文。 |
| P1 | expiring 被查询拒绝，expired 被扫描排除 | 可用凭据导致同步误停；临时故障后永远无法恢复 | 使用状态与时间双校验；刷新端口允许符合策略的过期 access，不能复用只取有效 access 的端口。 |
| P1 | 429/网络/平台事故三次触发 needs_reauth | 全平台同步误停，诱发无效人工授权和重试风暴 | 分类计数、期限预算、平台级冷却、带 jitter 的持久退避；授权状态不随临时失败次数改变。 |
| P1 | 成功通知丢失、失败后扫表重建任务、恢复批量 enabled=true | 重试不遵守退避，或擅自恢复管理员暂停的任务 | run/资格状态与 Outbox 同事务；阻塞原因归属+授权代次；成功事件幂等唤醒。 |
| P1 | 仅按“换钥后 90d”移除旧 key，或只保留两代 token | 当前静态凭据、历史凭据/PII/索引无法解密或查询 | 按实际密文及索引依赖盘点；清理只处理到期 retired 版本；多进程换钥演练。 |
| P1 | 只测 service/queue 列表，未测实际 API→worker 路径 | 发布后任务永不执行；重复投递/重启行为未知 | 扩展隔离 schema/Redis 的 S3 路径，断言真实刷新次数、版本链、审计、恢复与清理。 |

## 3. 方案对比

各题给出可供裁决的方案。人日均为增量设计落实、实现和该部分确定性测试的估算；不代表本次会实施。

### Q1 · 一次性 refresh_token 的幂等

**直接回答：**平台返回新 token 而写库失败时，进程仍持有响应就重试“加密持久化/发布同一结果”，不得再调 Refresh。若新值既未持久化又随进程丢失，本地只剩已消费的旧值；没有上游幂等结果回取/宽限期/对账能力就无法自动恢复，必须重新授权。pending 能记录事实和限制重试，不能消除这个不可原子化窗口。

| 方案 | 做法 | 优点 | 缺点 | 影响面 | 工作量 |
|---|---|---|---|---|---|
| A · 最小失败即人工 | 锁+校验 current_version_id；凡可能已发出的请求失败均标结果未知，停止该轮自动重试并要求人工处理 | 改动少；不会盲用一次性旧值 | 进程尚持有新结果也可能失去自动恢复机会；可用性低；仍需持久未知状态 | shop 刷新服务/run、适配器错误、运维流程 | 1–2 人日 |
| **B · 持久意图+加密结果暂存+对账（推荐）** | 请求前落 pending；发出前 CAS 标记 dispatching；成功响应先加密暂存，再短事务发布；对账按阶段恢复，未知且无平台回取能力时人工重新授权 | 数据库恢复后可发布同一结果；可观测、可防双发；明确剩余故障边界 | run 需扩列/内部阶段与约束；仍无法保证“平台成功但落任何存储前进程死亡”可恢复 | shop 公共刷新端口、bootstrap 持久适配器、新迁移、Outbox、手动 API | **4–6 人日** |
| C · 以上游幂等/宽限为主 | 为每个逻辑轮换固定 operation_id；仅在平台保证重用同键能取回同一结果或旧值安全重用时重发/回取 | 有文档保证时可跨进程补齐消费与落库窗口 | 三个平台不能假定都支持；本地 Idempotency-Key 不会自动变成平台幂等键；不能作为通用保底 | 平台能力/协议版本记录、适配器、run.operation_id | 每个平台额外 1–3 人日，前提是平台确有该能力 |

**B 的最小持久模型（建议扩入 credential_refresh_run，不另建一个“通用工作流系统”）：**

| 维度/阶段 | 持久事实 | 重启后的允许动作 |
|---|---|---|
| 轮换身份 | tenant_id、credential_id、base_version_id、operation_id、attempt/dispatch 代次、trigger、triggered_by/自动维护授权代次、next_retry_at | 同一输入版本只允许一个未决逻辑轮换；实际每次 HTTP 尝试保留独立记录，重投不新增 attempt。 |
| prepared | 已批准意图，尚未跨发送屏障；审计已提交 | 重验身份/当前版本/期限后可认领；队列丢失由持久意图再投递。 |
| dispatching | 以当前代次 CAS 获得唯一发送资格；随后才允许 HTTP | 请求可能已消费；即使租约过期也不得直接把旧值交给新 worker 重发。 |
| staged | 新值已密文持久化，带候选 version_no、nonce、key_version、过期元数据；不对使用端口可见 | 只恢复本地发布，不重新调用平台；暂存 AAD 仍按已保留的候选版本使用既有五维规则。 |
| published | 输出 version_id、完整发布事务已提交 | 返回同一安全结果；重投只读，不再产生版本或刷新调用。 |
| outcome_unknown | 无可信结果，可能已消费；保留平台安全 request_id 和原因 | 有已核验平台能力才对账/回取；普通 Check 只证明 access 是否可用，不能找回丢失的 refresh_token。无能力则转人工处理。 |

- `refreshing` 是运行阶段的展示，不新增 `shop_credential.status='refreshing'`。原 run.status 可保留四态，细节使用经批准的 phase/outcome 字段；未知未决不得伪装成可自动重试的 failed。
- 短事务一提交“意图/发送资格+读取审计”；事务外调平台；收到结果立即 Seal 并短事务暂存；短事务二原子提交“新版本、旧版 retired、current 指针、期限/失败计数、run 成功、refresh 审计、Outbox”。任何 SQL 事务都不跨 HTTP。
- 发布以 base_version_id、运行代次、凭据未被禁用、维护授权仍有效为条件。人工替换/撤权使旧运行失效；迟到结果只受控暂存/废弃，不能覆盖新指针，不能给废弃密文重分配版本号/AAD。
- 发布提交返回超时也可能已提交：按 operation_id/输出版本读回核实，不能直接再插 v+1。HTTP 成功但暂存失败时，使用有界收尾 ctx 重试保存同一密文；若仍失败则告警，进程死亡后的缺口必须如实承认。
- pending/发送屏障本身写失败时禁止调用平台；平台成功后新结果无论为何未发布，不能回滚平台外部事实。Outbox 解决本地提交与投递，不提供第三方 OAuth 的 exactly-once。
- 建议手动触发使用 `Idempotency-Key`：占位、意图、Outbox 和 HTTP 202 安全回执同事务提交；已接收但尚未发布时同键仍回202与同一 run_id，仅占位事务未完成时用10004，同键不同请求用10006。队列只带 ID/代次；不用 Redis 作新 token 的唯一暂存地。
- 若选择保留“手动立即生效”，HTTP 可等待同一持久轮换发布成功后返回200，超时后通过安全 run_id 查询；仍使用完全相同的发送屏障与版本发布服务。相较202，需处理请求寿命和API/worker双入口协调，不能省略 pending/对账。

**验收门禁：**发送前故障可安全重领；发送后响应丢失不盲刷；返回后暂存/最终提交分别故障可恢复；提交确认丢失不重复发；重复消息/不同 HTTP 幂等键不能绕过同一输入版本的未决屏障。

### Q2 · 并发与锁粒度

**推荐锁定完整授权凭据：**`(tenant_id, credential_id)`。一行凭据的加密 values 应包含同一授权的 access_token 与 refresh_token，二者同版本发布；资源 product/order/inventory 共享这把刷新锁，各自原有同步锁仍按既定规则工作。

| 方案 | 做法 | 优点 | 缺点 | 影响面 | 工作量 |
|---|---|---|---|---|---|
| A · 按店铺 | 一个店铺所有凭据共用刷新互斥 | 简单；能防 access/refresh 被错误拆行时的部分竞争 | 不相关的 cookie/key/OAuth 也互堵；多凭据等待长；跨店铺共用授权仍防不住 | worker 锁、shop 刷新查询、手动刷新入口 | 1–2 人日 |
| **B · 按凭据/授权单元（推荐）** | 每个完整授权 bundle 一把租约；定时、手动、按需均进入同一持久轮换；发起前和发布时校验版本/代次 | 允许独立凭据并行；同店多资源只刷新一次；契合现有多凭据行与不可变版本链 | 必须确认一行就是一个授权单元；租约之外仍需 Q1 的发送屏障 | shop 端口、bootstrap 凭据读写、手动 Rotate/Disable 协调、run 唯一约束 | **2–3 人日** |
| C · 按店铺+kind | 同类型凭据共锁 | 比整店锁更细，键容易构造 | kind 只有 oauth2/api_key/cookie/session，同店允许多个同 kind，既过度互斥又不能描述共享授权；access/refresh 不是两个 kind | 扫描器、锁命名、凭据选择策略 | 1.5–2.5 人日 |

- 建议 Redis 键 `lock:cred:{tenant}:{credential}`，租约初值 120s、30s 续租、单次平台预算沿用 15s，单次执行收尾预算建议 ≤60s；等待退避释放租约，不持锁睡眠。实际阈值配置化并以故障测试验收。
- DB 短事务保持 M2 的 shop→credential 加锁顺序；长期外部调用不持有这两把行锁。Redis owner 校验用于释放/续租，DB 代次用于拒绝迟到写，二者不能相互替代。
- **发布 fence 不能阻止已发出的第二次平台请求。**关键是持久 dispatching 屏障：失租只取消本地后续发送资格；已发送操作留待 Q1 对账，不能因 TTL 到期新建一次调用。相同 base_version 重复任务合并/记录 skipped，不递增失败次数。
- 人工“刷新”完全共用此流程；人工“替换凭据”或 Disable 也必须参与持久代次协调，使旧 worker 失去发布资格。对已经发出的外部请求只能隔离迟到结果，不能声称取消能撤销平台轮换。
- 读取时返回明确 credential/version 标识、expires_at 与能力相关元数据，不能靠现有“店铺下 id 最小的 active 行”推断刷新目标；不得趁轮换默默切换另一个账号。本期不做主备切换。
- 旧版本 Fetch 的401/403迟到时先重读 current_version_id：若已换新，仅允许用新版本重试一次，不再刷新旧版本；旧响应不得递增新版本失败计数或把当前凭据改成 needs_reauth。错误处置也必须携带并校验输入版本/代次。
- 若历史数据把同一 refresh 授权拆成多行，先识别授权归属并形成完整 bundle；同一上游 grant 跨店共享时必须按真实 grant 串行或禁止自动刷新该记录。未经明确迁移裁决，不声称按 credential_id 已覆盖这种共享关系。
- 刷新期间旧 access 尚有效且平台保证其继续有效，可在安全余量内照常 Fetch；若平台可能立即撤销旧 access、已经过期或已收到可信失效信号，则转等待凭据。同步任务释放执行片/资源租约后延迟重排，不占 worker 阻塞等待刷新。

**验收门禁：**两个资源、两个 worker、手动与定时同发只调用平台一次并发布一版；租约失效后旧请求迟到不能覆盖人工新版本；不相关凭据可并行；等待刷新不占住同步执行片。

### Q3 · 失败分类与退避

**先判定请求结果是否确定，再判定失败原因。**“可以重试某一类错误”不等于“可以再次使用可能已被消费的 refresh_token”。

| 方案 | 做法 | 优点 | 缺点 | 影响面 | 工作量 |
|---|---|---|---|---|---|
| A · 沿用任意三次失败 | 网络/429/5xx/授权错误累计同一计数，第三次 needs_reauth 并停任务 | 贴合现 PRD 原验收；实现短 | 一次平台事故误伤全部店铺；重授权不能修基础设施；长退避不可达 | 原 run/credential 字段、通知、停任务接线 | 0.5–1 人日 |
| **B · 分类+结果确定性+有界退避（推荐）** | 临时失败计运营连续失败；确定授权失效立即人工；未知结果进入对账；预算耗尽走告警/冷却而非伪装授权失效 | 处置匹配原因；保护一次性 token；故障恢复不依赖反复授权 | 需修订 REQ-SHP-008；适配器必须给安全、稳定的分类证据 | Connector 刷新错误契约、run 元数据/next_retry、扫描资格、告警 | **1.5–2.5 人日** |
| C · B 加完整平台熔断状态机 | B 之外持久化平台级开/半开/关与探测额度 | 大规模平台事故时减少流量 | 跨实例探测、配额维度与恢复测试增加；规模未明时成本偏高 | 平台级运行状态、调度配额、指标与运维配置 | 3–5 人日 |

| 输入类别 | 平台适配器必须区分的事实 | 重试与人工边界 | 同步动作 |
|---|---|---|---|
| 401/403 | Fetch 的 access 过期；Refresh 的 invalid_grant/撤销；scope 不足；应用密钥/签名/时钟配置错误不能混为授权失效 | access 过期可请求一次合并刷新；确定 refresh 失效直接 needs_reauth；权限/应用配置由管理员修复，禁止循环刷新；仅状态码不足以做终判 | 只阻塞依赖该凭据/能力的调用；scope 不足按受影响资源处理；不修改 shop.status |
| 429 | 读取 Retry-After 的秒数或 HTTP 日期、平台业务限流码与实际 app/shop 配额维度；确认这是未签发新凭据的拒绝 | 确定未消费才可重试；等待不得早于 Retry-After，同时加正向 jitter；缺省建议 60s；不计授权失效 | 有效旧 access 可用时遵守同一已知配额/冷却；过期则等待，不用刷新风暴争抢额度 |
| 5xx | 请求未发送、明确未消费，还是服务端可能已成功处理后返回错误 | 前两者有界退避；可能消费则 outcome_unknown；平台恢复不自动消除未决消费事实 | 有效旧 access 按平台保证使用；到期/无有效性保证则阻塞；平台故障不触发整店禁用 |
| 网络超时/断连 | DNS/连接失败且可证明未发送；发送中/响应读取超时；成功响应解析失败 | 可证明未发送才重试原输入；已发送或无法证明就未知。成功报文缺必需新 refresh 值也可能已消费，不可“解析失败后再刷” | 保留检查点；等待刷新/对账，不推进 Fetch 失败页 |
| 平台要求重新授权 | 取消授权、refresh 到期、授权总期限已到、账户断连等平台确定信号 | 不自动重试；立即 needs_reauth；人工重新授权提交新版本后复位 | 阻塞依赖凭据的同步并发站内待办；不把商品/订单数据清空 |

- B 保留卡中 `1m/5m/30m/2h/6h` 为“第 1–5 次可安全重试前的基础间隔”，即首次调用后最多五次重试；采用例如 0–20% 正向 jitter。临时失败计数、实际 HTTP attempt、逻辑轮换结果、授权失效原因分别记录；锁忙/重复投递/权限撤销不算平台失败。
- 下一次资格为“不早于业务退避与平台 Retry-After 的较晚者”，写库并随 Outbox 发布；扫描、手动重复触发都不能绕过已有未决轮换的 next_retry_at。适配器/HTTP/asynq 不再叠一层隐式 Refresh 重试，asynq MaxRetry=0。
- 预算受 access 到期、refresh 到期、授权总期限以及运行总次数共同限制；Retry-After 超过剩余期限时告警并等待/人工，**不缩短平台等待时间强行重试**。access 过期只改变同步可用性，不等于 refresh 资格消失。
- 建议保留 shop_credential.expires_at 的 access 有效期语义，增量记录 refresh_expires_at、authorization_expires_at 及期限是否已知；未知值不能解释成永久有效。相应元数据、运行资格与索引纳入新增迁移/端口提案，平台未返回时只使用已核验规则。
- 连续三次临时失败建议升级告警；重试耗尽记 exhausted，下一步由批准的冷却探测或人工重试开启新轮，不能被每分钟扫描无限重置预算。只有明确可安全重发的操作才能探测。
- 本地 DB/Redis/审计失败、30501 解密失败归基础设施/部署故障，不计“平台授权失效”；审计失败不调平台，解密失败告运维。沿用已有 30302/30303/30501、10004/10005/10007/10002 等语义，不复用身份登录刷新码 20109；新增对外错误语义须另行获准。
- 空 refresh_token 仅在该平台协议明确表示“保留输入 refresh_token”时合并；对一次性轮换平台属于异常结果。expires_at 缺失仅允许使用已固化协议规则，不能把缺失当静态凭据或无期限 active。

**验收门禁：**五类输入与各自“确定/未知”组合；三次 429/5xx 不 needs_reauth；首次明确撤权即停止；Retry-After 不被 jitter/期限预算提前；重启后预算与下次资格不归零。

### Q4 · 历史保留与密钥

**保留规则已经裁决：**正式版本元数据永久保留，retired 密文/nonce 保留至 `retired_at+90d`；当前版本不因创建满 90 天被清理。代数随轮换频率变化，不按“最近两代/三代”截断，例如每四小时轮换约保留 540 个历史版本加当前版本。可解密不代表平台仍接受，已消费 refresh_token 绝不能用于业务回滚。

| 方案 | 做法 | 优点 | 缺点 | 影响面 | 工作量 |
|---|---|---|---|---|---|
| A · 现有 90d 字段+人工维护 | 沿用 M2 retired/purge_after，发布时人工盘点保钥与清理清单 | 不改变存储政策；开发少 | 高频轮换和多进程发布后清单易过时；清理/退钥缺自动证据；不能把“已设置 purge_after”当作已清理 | M2 既有字段、部署操作手册、人工审计 | 0.5–1 人日 |
| **B · 90d 清理+引用盘点（推荐）** | 同样保留期，补幂等分批清理、purge 审计、密文引用/密钥依赖核查与告警；部署验证所有读取者持有所需版本 | 满足现行 ADR 且可验证；静态凭据不会被误清；可证明退钥前提 | 增加维护任务和部署检查；当前长期凭据仍引用旧 key 时该 key 必须继续保留 | shop 维护端口、bootstrap 清理装配、keyring 配置验证/运行证据 | **1–2 人日** |

- 业务 `credential_version.version_no` 与加密 `key_version` 独立增长；Refresh 产生业务新版本并用活动 key 写入，历史 Open 只按其存储的 key_version 读取，不能试遍 keyring、不能改 AAD。
- 换钥顺序：先让 API、所有 worker 和维护进程都能读旧+新 key；确认部署后切活动写版本；仍保留旧读钥。Keyring 是启动时构造的不可变实例，不能假定改环境变量即热更新。
- 退钥不是“切写后等 90 天”：须确认没有当前凭据、90d 窗口内历史、未发布暂存结果引用它；共享 keyring 的订单地址、加密 raw、询价以及派生索引密钥也必须纳入部署依赖清单。M7 只做检查/阻止误退，不改这些模块的加密/索引机制，不做 KMS 或批量重加密。
- 清理必须同时核验 retired_at、purge_after、非 current 指针，按批清空到期 cipher/nonce，保留 version 元数据并记 purge 审计；失败可重试，不能硬删版本/审计链。备份副本的销毁不由在线清理自动保证，部署保留策略需覆盖它。
- 暂存成功转正式版本时同事务清掉暂存副本；未发布但已明确废弃的敏感结果建议 24h 内清除密文，保留非敏感处置证据，不能借此缩短正式版本 90d 规则；未决结果的期限/处置须进入待办，不能静默清掉唯一可恢复材料。
- 历史最小证据：operation/run/attempt、trigger、actor/授权代次、worker 与 trace_id、安全平台 request_id、输入/输出 version_id/no、key_version、阶段/结果确定性、归一原因、白名单平台业务码/HTTP 状态、耗时、expires_at、next_retry_at、发布/废弃时间。元数据和指标不得出现 token 明文、密文全文或新拼接的 secret 指纹。
- 刷新响应仅在受控内存和密文暂存/正式版本中存在；raw_meta、audit.detail、错误链、死信、Outbox、响应头和通知只能放白名单元数据。展示沿用安全 DTO 与既有 fingerprint，不增明文读取接口，也不把“审计排障”变成 token 导出。

**验收门禁：**旧 key 可读/新 key 写入、两个业务版本独立解密、丢钥返回安全 30501、到期 retired 才清密文且有审计；当前版本/未决结果不误清；轮换失败不改变既有版本内容。

### Q5 · 与同步的关系

**推荐按凭据可用性决定同步资格。**过期/撤销/未知且无有效性保证时停止平台 Fetch；仍有平台确认有效的旧 access 时允许继续；重新授权、平台配置错误、基础设施故障分别显示原因。刷新不承担业务只读/写权限降级，也不修改历史数据。

| 方案 | 做法 | 优点 | 缺点 | 影响面 | 工作量 |
|---|---|---|---|---|---|
| A · 整店暂停，人工恢复 | 任意刷新阈值触发停所有 sync_job；恢复由人逐项确认 | 与原卡接近；恢复责任明确 | 一份凭据误伤不依赖它的资源；无自动恢复；若自动全启则覆盖管理员意图 | sync_job 管理端口、运维待办 | 1–2 人日 |
| **B · 依赖凭据阻塞+幂等唤醒（推荐）** | 保留 enabled 管理员开关；另记 credential 阻塞归属/版本与等待的 job/run；成功发布后只解除自身阻塞并重验授权 | 故障隔离精确；保留检查点与管理员暂停；可安全自动恢复 | 需批准最小同步接线及持久阻塞事实；现有代码不足以只改 shop | shop 公共端口、Sync 的资格/延迟/恢复端口、bootstrap、Outbox、工作能力 | **3–4 人日** |
| C · 降级只读调用 | 若平台另有仍有效的只读凭据/权限则仅开放这些动作 | 某些平台上可保留部分功能 | 无效 token 对读接口同样无效；需额外授权/凭据选择，易引入本期禁止的主备账号；同步本来多为读上游 | 平台能力、动作权限、额外凭据配置 | 另计 3–6 人日，当前不推荐 |

**刷新身份：沿用 WorkAccess 机制，现有 Go 类型不能直接套用。**`IssueWorkAccess`/`WorkFrom` 强制正数 job/run/mapping_version，资源仅 product/order/inventory/category；刷新没有业务资源或映射版本，不能伪造 job_id、借某个映射 ID，也不能给 worker 一个 all 用户。

- 推荐新增同一能力体系内、独立验证的 `CredentialRefreshAccess`：tenant、shop、credential、base_version、operation/run/attempt、维护授权代次/运行代次、trigger、actor，以及最小 load/stage/publish 方法集合；保留现有同步 WorkAccess 的所有强校验，不将 mapping_version=0 当通用豁免。
- bootstrap 从可信任务 ID 重读租户→店铺→凭据→运行关系后签发，能力不可由 HTTP/队列 JSON 构造；发送前、每次重领、发布前重验。只持该能力不能写 raw、游标或规范业务表；同步使用凭据的能力也不自动获得轮换发布权。
- 手动：HTTP 先检查 `shop:credential:rotate` + 当前 shop 范围并持久化发起人；发送前及发布前重载发起人权限/范围，撤权、移交即失效。后台审计 actor_type=system，保留原始发起人/trigger，不冒充在线用户。
- 定时：以有权人员明确启用 auto 维护为授权事实，持久保存批准记录/代次，不依赖 sync_job 的 enabled 或批准人仍在线；关闭 auto、禁用凭据、禁用/删除店铺、店铺移交使旧代次失效。历史 auto 行无批准来源时须一次性核对批准，不能无条件放行。
- 建议店铺 paused（仅暂停同步）时仍维护已批准的凭据，disabled/删除时不发平台请求；同店单个 job 停用不影响刷新，否则停同步期间会把仍可维护的授权拖过期。此资格差异必须显式记录。
- 刷新读取必须按指定 credential/base_version，允许 access 已过期但 refresh 授权有效；现有 UseCredential 只供有效 access 消费，不能直接复用为刷新读取。读取审计提交失败时不调用平台。
- 若发送后发生撤权/移交，平台可能已消费旧值：允许受控保存已收到的加密结果以避免丢失，但旧能力不得激活/继续调用；经当前授权人员处理。恢复任务资格与密文保存的权限边界分开。

**同步恢复与事件：**

- Fetch 前凭据不可用时登记持久阻塞，释放执行片/资源租约，保留既有页/订单详情待办；不推进 Fetch 失败页的 sync_cursor，不改变 ADR-019 的双进度。
- 新版本发布成功同事务发出只含 ID/版本的 credential 可用事件；以 credential_id+output_version 去重，消费者重验管理员 enabled、阻塞原因、shop/授权代次，只唤醒依赖该凭据的等待任务一次。
- 已终态失败的 run 不直接改回 running；通过现有公开启动/重试端口创建有来源的新运行，继续现有游标/详情待办。原人工任务撤权后不能借系统刷新成功绕过授权复跑。
- 刷新失败只推进自身退避/对账和通知；不立即唤醒同步重试同一个无效 token。基础设施恢复、人工提交新凭据或成功发布新版本后再评估唤醒。
- 既有离线 raw 重放不需要平台 token，不应仅因 token 到期被刷新逻辑连带停掉；其原有权限、grant/fence 与来源规则照常执行，不新建另一套重放调度。

**需批准的最小范围白名单（本次不修改）：**shop 刷新/使用公共契约及原 Rotate/Disable 协调；`internal/kernel/scope` 目的限定能力与登记；`internal/modules/connector` 刷新端口/适配器；`internal/modules/sync` 凭据等待/恢复端口与对应 repo 契约；`internal/bootstrap/shop_credential*`、`sync_runtime.go`、`sync_run.go`、订单消费凭据的装配入口、`worker.go`；`internal/worker` 任务路由；新增迁移/测试及 S3 刷新场景。仅改接线与资格，不改 M2 加密、来源仲裁、重放算法。

**验收门禁：**手动撤权/移交/停 auto 即撤销后续发送或发布；paused 店铺仍能保养已批准凭据；refresh 能在无 mapping/job 时运行；成功仅恢复自身阻塞；管理员暂停保持；两个消费资源无重复立即重试。

### Q6 · 观测与告警

预警需区分“access 将过期但可自动维护”“refresh/授权总期限将尽”“轮换已无法恢复”。所有持久时间用 UTC，09:00 汇总的展示时区单独配置，不能让每日一次扫描承担短 TTL 凭据的唯一预警职责。

| 方案 | 做法 | 优点 | 缺点 | 影响面 | 工作量 |
|---|---|---|---|---|---|
| A · 三渠道逐凭据状态提醒 | 站内信、邮件、Webhook 均实现，按每份凭据/状态单独发给有权处理人 | 满足 PRD 渠道要求；简单直接 | 平台事故时大量同因告警；普通 access 临期可能天天噪声；缺少卡住/调度失活检测 | notifier 端口、三渠道、模板、notification 管理接口 | 1–2 人日 |
| **B · 站内待办+分层聚合外部提醒（推荐）** | 同样提供三渠道；站内为处置主记录，按配置投递 Webhook/邮件；平台事故聚合，凭据未知/需重新授权单独升级；补运行与调度指标 | 负责人能处理，运维能看故障面；外部内容最少；具备恢复与送达可见性 | 需收件人校验、持久去重、通知重试/已读状态；配置和阈值要验收 | A 的范围+指标/调度心跳、聚合/滚动去重、目标授权核查、新增迁移字段 | **2–3 人日** |

- 每分钟检查 due/过期/卡住/重试逾期；人工处置按 **3 天、1 天、已过期** 分档，首次进入和升级即时入站内待办，24h 内同档不重复；09:00 仅补充汇总。
- 自动短 TTL access 的正常 expiring 状态计指标，外部只在刷新逾期/连续失败/恢复预算不足时升级；refresh 或授权总期限临期照常通知负责人。这是对原“所有 expiring 每天提醒”的降噪建议，需 Q6 一并批准；静态 key 不刷新，无期限不假造过期日，cookie 仅预警。
- refresh_at 只减一次平台维护 lead_time，再按 now 扫描。lead_time 至少覆盖扫描延迟+队列预算+15s 调用+时钟余量及选定重试机会；短 TTL 示例可从提前 30min 起评估，不能统一提前三天。临期剩余时间覆盖不了下一次合法重试就立即预警。
- 自动维护缺 refresh_at、refresh 期限未知、期限解析异常必须进入检测清单；不能因为 NULL 不匹配 SQL 而静默消失。

| 指标/信号 | 口径 | 建议初始阈值与动作 |
|---|---|---|
| 逻辑刷新成功率 | published 逻辑轮换 / 已达到判定时限的逻辑轮换；未知计未成功，skipped 不入分母；另报 HTTP attempt 成功率，避免重试抬高分母 | 15min 样本≥20 时 <95% 持续5min warning，<80% 持续5min critical；小样本使用单凭据信号 |
| 连续临时失败 | 同一输入版本实际 HTTP 尝试的连续失败，不含队列重投/锁忙 | ≥3 次 warning；持续30min 或耗尽重试预算且临近到期 critical；不据此改 needs_reauth |
| 授权失效/结果未知 | 新进入 needs_reauth / outcome_unknown 的记录 | 任一条立即生成负责人待办；结果未知与临近/已过期升级外部 critical |
| 卡在 refreshing | prepared/dispatching/staged 的年龄、owner 心跳与 lease_until | prepared/staged 超60s warning、超120s critical；dispatching 超15s调用+收尾预算应调查，超120s/失租转对账，绝不据此自动二次发送 |
| 调度/退避积压 | 最后成功扫描年龄；now-next_retry_at；最老 due 任务等待 | 扫描 >120s warning、>300s critical；重试逾期或 due 等待 >120s warning、>300s critical |
| 凭据可用性 | 依赖任务存在且无有效 access；refresh/授权剩余寿命；30501/审计失败数 | 已过期且阻塞同步立即告警；剩余寿命不足合法重试预算立即升级；解密/审计失败发运维 |
| 通知送达 | pending 最老年龄、failed 数、渠道成功率 | pending >5min warning；重试3次仍失败转 failed，站内可见并以其他已配置通道升级，不影响刷新已提交事实 |

- Prometheus 标签仅用 platform、trigger、outcome/error_kind 等有界集合；credential/shop/user/trace/request_id 放受控日志或站内记录，不作高基数标签。日志只记录白名单 ID/阶段/耗时/安全原因，不记平台 URL 查询串、认证头、响应全文、token/fingerprint 作为诊断标签。
- 收件人：业务待办给当前有店铺范围及处理权限的 owner/店铺管理员；应用配置、DB/Redis、密钥故障给批准的租户运维。店铺移交、离职、撤权后重验目标及站内读取权限，不向旧负责人继续泄露资源信息。
- 站内内容仅内部资源 ID、原因分类、剩余时间、run/trace 关联和需登录再鉴权的处理链接；外部仅事件 ID、平台码、严重度、内部处理入口。不得附 token、secret、原始请求/响应、邮箱/手机号/地址等 PII。邮件地址、Webhook URL/签名密钥属于受控渠道配置，不进 payload/日志，敏感配置复用现有密文能力。
- notification.target 建议引用用户/受控渠道配置ID，发送时解析已批准目标；不能将带 token/签名参数的 Webhook URL复制到通知行、错误或出站诊断中。站内读取同时检查当前资源范围，只有收件人ID匹配仍不足以授权。
- dedup_key 必须区分 tenant、凭据/事件、预警档位、渠道、收件目标标识；用最近成功/有效待投递时间做原子滚动24h判定，不能只拼日期（跨午夜会连发）。严重度升级与恢复有独立事件，同档重投复用记录；不把站内成功当邮件/Webhook 已发送。
- notification 增量字段需支持 read_at、可重试时间与滚动去重事实；现有 sent 是送达状态，不能当已读。通知与刷新结果通过 Outbox 对接；外部至少一次投递可能重复，Webhook 附稳定事件 ID 供收件方去重，不能承诺 exactly-once。
- 三渠道实现与配置在本期内；仅对已批准的地址/端点投递，使用有界超时和重试。只实现 credential.expiring/needs_reauth/结果未知/恢复等本题模板；不顺带实现 drift、billing、sync 的完整通知业务。

**验收门禁：**假时钟跨 3d/1d/expired、午夜/滚动24h、并发通知去重、多渠道不互相抑制；旧负责人不可读/不再收件；通知失败不回滚已成功刷新；告警与所有错误/队列输出无敏感材料。

### 3.7 推荐组合的工作量与交付门禁

推荐 Q1=B、Q2=B、Q3=B、Q4=B、Q5=B、Q6=B；六项合计 **13.5–20.5 人日**，跨进程/故障注入/迁移联验另计 3–5 人日，首个真实平台刷新协议固化另计 1–2 人日，合计约 **18–28 人日**。Lazada/TikTok 本次仅做差异评审，不把新增完整平台接入算进 M7。工期不是平台能力缺失时的强恢复承诺。

建议在同一 M7 内按顺序验收：持久状态/契约→刷新与并发恢复→同步接线→通知/清理→S3 进程联验；每个实现 PR 仍只交付批准的一张任务卡。若不批准扩展白名单，应先重写 M7 交付边界，不能声称原卡完整功能已能在只新增 refresh*.go 的条件下交付。

S3 联验需在原随机 schema、独占 Redis DB、随机端口与可靠清理路径上增加：

1. 随机生成合成 OAuth 值的 mock 刷新服务，维护“一次性 refresh”状态与请求计数；不把实际 token 写进 fixture、输出或日志。
2. 真实 API 手动触发/扫描入队→正确 refresh 队列/处理器→两个 worker 竞争→版本发布→同步恢复；断言平台调用数为1、只新增一版、有读取/刷新审计。
3. 依次注入发送前失败、平台已消费响应丢失、成功响应后暂存/发布 DB 失败、进程退出、锁丢失、人工替换、授权撤销；对未知结果断言不自动再次消费。
4. 校验退避持久性、旧 key 读取、新 key 写入、retired 清理、三渠道本地替身、泄露六通道与清理零残留。用假时钟/有界轮询，容量统计仅显式开关执行；race 依现环境在 CI 验证。
5. 集成测试以 Credential/Sync/Worker 等对应域前缀命名，交由现有分片守卫验证恰好覆盖一次；不能仅看到 Inspector 的 refresh 队列就宣称功能通过。

## 4. 未来 2 个里程碑的坑（只列不展开）

- **M8**：网关读同一上游凭据时若绕过凭据使用端口，将与后台刷新争用旧版本并重演消费未知窗口。
- **M8**：代理对认证 URL、请求头或上游错误做通用日志记录，可能把刷新 token/应用密钥带出白名单边界。
- **M8**：将平台 401/403 都当客户端无权限，会掩盖上游授权失效并导致无效代理重试。
- **M9**：因凭据等待而延迟/失败的上游调用若直接按 HTTP 重试次数计费，会重复预扣或把未完成调用当完成。
- **M9**：通知若把上游凭据事故与余额不足共用一个禁用/恢复开关，会错误恢复欠费或管理员停用的任务。

## 5. 待决策清单

以下均是请求人裁决的提案，不是已经生效的 ADR 或实现指令；同意后由人将结论追加到 ADR/修订任务卡，不能由本次评审直接改既有结论。

- **Q1**：是否选 B，批准持久阶段/加密结果暂存与对账，接受无平台能力时“已消费且结果丢失必须重授权”的边界，以及手动触发改 HTTP 202+安全 run_id 的契约？
- **Q2**：是否选 B，以完整 OAuth bundle 的 tenant+credential 为刷新互斥单元，所有自动/手动入口共用发送屏障，人工替换/禁用参与代次协调，共享 grant 未厘清的记录暂不开自动刷新？
- **Q3**：是否选 B，将“三次任意失败→needs_reauth”修订为“三次临时失败告警、确定授权失效立即人工、未知先对账”，并批准最多五次安全重试和持久预算？
- **Q4**：是否选 B，在已定90d政策下补齐清理/引用盘点/退钥检查；废弃暂存密文建议24h清理，未决唯一结果须显式处置，正式历史不因代数提前删除？
- **Q5**：是否选 B，批准目的限定的 CredentialRefreshAccess、维护授权记录/代次、paused/disabled 的资格差异，以及 §3 Q5 的最小同步/worker/装配改动白名单，恢复时保留管理员开关与原检查点？
- **Q6**：是否选 B，交付三渠道适配器、站内处置主记录、分层外部提醒、滚动24h去重与指标阈值，并接受正常短 TTL 自动维护临期只计指标、异常/人工期限临期才升级外部提醒的口径？
- **配套裁决 D1**：是否接受约18–28人日，并按实施时最大迁移号+1新增（当前从000015起），将平台当期协议/fixture验证作为真实自动轮换的上线门禁？

本次验收仅检查评审内容与文件范围；未执行 make lint/test、迁移或平台刷新，不声称这些实现验收已经通过。官方资料核验限制见 §7。

## 6. M7 会暴露的 M2/M3 遗留问题

下列“已确认”指代码静态证据，不代表已复现线上事故；修复范围仍需本次 Gate A 裁决。M7 不借此重开加密、WorkAccess、来源仲裁或重放机制。

| 归属/证据 | 已确认现状或待核问题 | M7 所需最小处置 / 留后边界 |
|---|---|---|
| **M2 · 已确认**：`internal/bootstrap/shop_credentials.go` WorkerCredential；`internal/modules/shop/credential_use.go` CredentialDTO/UseCredential | 只查 active 且未过期的行，按 id 升序选一份；DTO 没有版本/期限；审计只有 job/run，未记录消费的 version_id | 允许仍有效的 expiring；明确使用/刷新凭据 ID 与输入版本，补可追溯元数据；不引入多账号主备。 |
| **M2 · 已确认**：`internal/modules/shop/credential_writes.go` RotateCredential；`internal/bootstrap/shop_credentials.go` updateCredential | 人工轮换写 active，但未写 refresh_at、last_refreshed_at、consecutive_failures；原恢复条件未落地 | 将重置计数、重新计算资格/状态、维护代次失效与可用事件纳入人工更新事务，不能只靠新 Refresh 路径。 |
| **M2 · 已确认**：`internal/bootstrap/shop_credential_writes.go` retireCredentialVersion；shop model 与迁移000004 | 已设置 retired_at+90d 与 purge_after 索引；所审维护路径未见凭据 purge 执行器，不能将字段存在当清理已运行 | M7 补维护装配和幂等审计；历史重加密/KMS 留后；退钥检查覆盖共享 keyring 依赖。 |
| **M2 · 待核授权数据**：Create/Rotate 的 refresh_strategy 与 ADR-015 权限边界 | auto 可作为元数据保存，但现有行缺少专用的自动维护批准记录/代次 | 上线前受控核对并批准存量 auto；凭据移交、关闭 auto、禁用产生撤销事实；不得假定曾创建就有永久后台轮换授权。 |
| **M3 · 已确认**：`internal/modules/connector/types.go`、`errs.go`；Shopee requestError | 实际没有 Refresh/Check 方法；Shopee 把 HTTP 401/403 都归 auth_expired；Lazada/TikTok 不在当前注册装配中 | 增量刷新端口与细分结果/能力；锁定首个平台协议；矩阵不等于三平台已接入。 |
| **M3 · 已确认**：`internal/kernel/scope/work_access.go`、`work_context.go` | 工作能力仅适配有 job/resource/mapping 的同步；强约束是既定安全条件 | 增加目的限定同族能力及声明，保留同步强校验；不得伪造 mapping/job 或复用人类全范围身份。 |
| **M3 · 已确认**：`internal/modules/sync/orchestrator.go` deferFetch；`internal/bootstrap/sync_run.go` DeferRun | auth_expired 终态失败且 enabled=false；无“因哪份凭据阻塞/由谁暂停”的恢复事实；与管理员停用混用 | M7 引入凭据阻塞归属与恢复端口，批准最小接线；不改 grant/fence、来源顺序或重放执行器。 |
| **M3 · 已确认**：`internal/worker/queue.go` Publish/NewQueueServer；`internal/bootstrap/worker.go` RunWorker | refresh 队列存在但没有 credential:refresh 处理器；非 sync Outbox 默认发 notify；当前调度源只有 sync_job | 新增刷新任务类型/路由/处理器或批准等价事件映射，Outbox 与扫描恢复链必须贯通；避免通知流量挤占刷新。 |
| **S3 · 已确认**：`scripts/smoke/setup.ps1:67`、`steps.ps1:1`、README | readiness 只验证实际 PID+四队列存活；凭据是 api_key，刷新未被覆盖；worker 无 HTTP readiness 是 S3 已批准差异 | 扩展刷新行为断言，不把补 worker HTTP readiness 作为本次前置或顺带任务。 |
| **通知基础 · 已确认**：迁移000001 notification；`internal/bootstrap/share_inquiry.go`、`share_event.go` | 已有询价 inbox/去重写入，尚不能代表 M7 通用三渠道、已读、滚动24h能力齐备 | 复用表和 Outbox，新增必要字段/模板与安全接口；保留询价回归，不在 M7 扩展整个平台通知业务。 |

## 7. 平台差异矩阵

**证据口径：**本次已检查仓库 Shopee 适配器与注册装配，确认现阶段只有商品 Fetch，没有刷新实现；Lazada/TikTok 不在该装配内。2026-11-09 尝试读取三个官方入口均返回 HttpRequestException，未取得可核验的当期正文。下表外部协议描述是**接入待核的协议画像，不是已验证上线契约**；不得据此写死 TTL、承诺旧 refresh 可重试，或宣称三平台均为一次性 token。

| 平台/画像 | 刷新协议与身份范围（目标版本仍需核验） | 期限与返回合并差异 | 轮换/重用及结果未知的边界 | 配额/错误与实现影响 |
|---|---|---|---|---|
| **Shopee Open Platform v2** | 候选端点 `/api/v2/auth/access_token/get`；区分 shop 与 merchant 授权，partner 身份、请求签名和授权主体不能混用 | 返回 access/refresh 与 `expire_in` 等元数据；相对秒需在适配器转换绝对 UTC；refresh/总授权期限需单独证明，不能从 access TTL 推导 | 接入前核验刷新是否替换旧 refresh、旧 access 是否同时失效、重复使用是否有宽限/同结果回取；未证明一律不对未知结果盲刷 | 认证请求签名与业务 Fetch 签名不宜照抄；解析业务 error/request_id，不能只看401；复用 shop/app 配额需依据官方规则 |
| **Lazada Open Platform** | 候选端点 `/auth/token/refresh`；Open Platform 的 app_key/sign、seller/国家站点与多店授权关系，不是 Shopee partner/shop 签名协议 | 需核验 `expires_in`、`refresh_expires_in` 及返回 refresh_token 的含义；区分“新 refresh 的剩余寿命”与“原授权总期限”，明确返回空/缺字段的合并规则 | 核验返回新 refresh 是否立即废旧、同一 seller 多站点是否共用 grant/消费额度；共享授权不能仅按每店凭据行独立刷新 | HTTP 成功仍可能是业务失败；归一业务码、request_id 与 app/seller 限流范围；不把所有403都记 needs_reauth |
| **TikTok Shop Partner API** | 候选端点 `/api/v2/token/refresh`；区分 Shop Partner 与普通 TikTok Login OAuth，两者不能混套；核验 app_key/app_secret、grant_type、地区/授权主体 | 需核验 `access_token_expire_in` / `refresh_token_expire_in` 的单位与含义，目标版本可能以绝对时间戳表达，不能因字段名 expire_in 就当相对 TTL；独立保存授权截止 | 核验新 access 是否撤销旧 access、refresh 是否轮换、刷新是否延长授权总期限及旧值重用结果；允许重复用不等于重复请求返回同一新值 | 认证域名/业务域名、业务 code/message/request_id、app/shop 配额由适配器处理；认证参数可能在 URL，日志必须丢弃 secret 查询串 |

三个平台共同遵循 Q1 的“一次性且无回取能力”最坏情况保护；若平台实际有更强恢复能力，只能在证据明确后开启对应能力，不能为了统一而丢掉差异。

**通用层可以抽象到：**

- 刷新意图、operation/attempt、输入版本、互斥/发送屏障、代次发布、加密暂存、不可变版本链、Outbox、重试资格、告警与审计。
- 标准化刷新结果：不透明的新 values、各自绝对过期时间、scope 等公开元数据、refresh 的替换/保留指示、旧 access 的有效性保证、平台安全 request_id。
- 标准化失败：原因分类+是否可重试+Retry-After+是否确定未消费/结果未知；以及平台经核验的能力：是否可刷新、授权粒度、旧 token 重用/宽限、幂等结果回取、配额维度和契约版本。
- 这些是本次建议新增的端口语义，**当前 Platform/Capabilities 不具备**；通用层不检查具体平台字段，不调用“猜测存在”的对账 API。

**必须留在平台实现：**认证端点/区域选择、签名规范、密钥取用、请求编码、响应字段解码、TTL/时间戳转换、业务错误码、refresh 缺失/空串合并规则、撤销/重用语义与配额映射。身份材料只在受控调用中解密；端点通过既有配置与经批准的适配器常量管理，不能散落在 service/handler。此分界不把订单/商品字段解释搬进 connector，也不改变既定映射机制。

**真实接入门禁：**为明确的 API 版本/地区/授权类型取得官方证据并记录文档日期，使用运行时随机合成 secret 的 mock/安全 fixture 固化正常刷新、缺字段、业务401/403、429、5xx、发送后超时、旧 refresh 第二次使用、旧 access 交叠及授权期限不续长的行为；真实平台验证只能在批准的测试授权上进行。没有证明“未消费”或“重试可回取”的平台，结果未知必须停自动消费并要求受控对账/重新授权，不能声称无损自动恢复。

官方核验入口（本次未取到正文，仅供后续定位，不能作为已经核实上述语义的证据）：

- [Shopee Open Platform Developer Guide](https://open.shopee.com/developer-guide/20)
- [Lazada Open Platform 文档入口](https://open.lazada.com/doc/doc.htm?nodeId=10804&docId=108066)
- [TikTok Shop Partner 文档入口](https://partner.tiktokshop.com/docv2/page/refresh-token)
