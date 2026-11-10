## M0 · 工程底座
- 完成日期：2026-10-31
- Tag：v0.M0
- 状态：完成
- 验收：§9 的 10 条全部通过（第 7 条由 engine_test 覆盖，真实停机演练移至 M10）
- 偏差：MinIO 改用二进制构建（镜像拉取受限）；Redis 本地映射 16379，仓库默认 6379 不变
- 下一个里程碑：M1a，入口 docs/milestones/M1a.md；M1.md 为共享规格
- 新会话必读：AGENTS.md → docs/04-conventions.md → docs/milestones/M1a.md → 共享卡列出的必读文件

## M1 · Gate A 裁决与拆分
- 裁决日期：2026-10-31
- 状态：文档已裁决，M1a/M1b 均待实现；未创建发布标签
- 决策：ADR-011（权限/外键/数据范围/缓存）、ADR-012（family/宽限/失效）、ADR-013（登录保护/分批）
- M1a：认证、会话、最小种子、菜单聚合、认证契约；独立验收合并后打 v0.M1a
- M1b：管理 CRUD、RBAC、数据范围、会话管理、版本键 + 同步 INCR；完成后打 v0.M1b
- 延后：outbox 失效重试交 M3 T3.11；bcrypt cost 12 的压测裁决交 M10 T10.1

## M1a · 认证、会话与菜单聚合
- 实现与验收日期：2026-10-31；覆盖 T1.0–T1.4，本地验收和远端代码 CI 均通过；已合并 main 并发布 v0.M1a。
- 前置查询优化：SessionRevoked 存在性 SQL、000003 撤销部分索引、50 条历史记录下 SQL 条数保持 1；迁移并发锁使用非阻塞探测，避免 concurrent index 的旧快照死锁。
- 前置 CI：[73e2905](https://github.com/omwk/go-admin-api/actions/runs/41130424607)、[02a8e09](https://github.com/omwk/go-admin-api/actions/runs/41132295629) 均成功，包含 go test -race ./...；密码比对移到事务外，事务内锁行重读核验。
- 最终代码 CI：[e4f6142](https://github.com/omwk/go-admin-api/actions/runs/41137020244) 成功，go test -race ./...、make build 和运行镜像检查全部通过。本地完整 race 也通过。
- T1.4：me/menus 同源 user 和 permissions，菜单与角色菜单关联各查询一次；排序/隐藏/禁用/button/父隐藏/深度规则均有自动测试。
- seed：权限→菜单→角色及角色授权→部门→admin 与用户角色关联；连跑两次行数和密码哈希不变；super_admin 的 data_scope=all 有不变式测试。
- OpenAPI：五个认证端点、请求/响应及 20109/503 语义；实际 HTTP 响应契约测试通过。
- 本地命令：make lint、make test、make test-integration 均通过；集成测试包含真实 PostgreSQL 隔离 schema、Redis 测试服务器、实际 CLI/make 目标与 API 进程。
- 最终集成汇总：kernel/db 2.721s、bootstrap 25.858s、cmd/cli 10.353s；全量 17 个测试包通过。详细证据见 M1a §6。
- 工具链：GNU Make 4.4.1、GCC、golangci-lint 已安装；未升级项目 Go 版本或新增 Go 依赖。
- 交接入口：identity.JWTAuth、identity.ProfileService、identity/permission/registry.go、bootstrap.SeedIdentity、cmd/cli seed、api/openapi/identity.yaml。
- 偏差：前置索引使用新增 000003，不编辑已提交迁移；补齐 race 下的登录短事务；M1b 未开始。

## M1a · 发布与交接（2026-10-31）

- 发布 Tag：`v0.M1a`（附注：`M1a: 认证、会话与菜单聚合`），指向 main 合并提交 `be6e200a5aaf21c0d9f0648c49d8df8f75c8bcc2`；标签已推送。
- 合并记录：[PR #1](https://github.com/omwk/go-admin-api/pull/1)。[PR CI](https://github.com/omwk/go-admin-api/actions/runs/41139331802) 与 [push CI](https://github.com/omwk/go-admin-api/actions/runs/41139106979) 均成功，覆盖 race、PostgreSQL 集成、构建与镜像检查。
- 本地复验：`make lint` 通过；`make test-integration` 的 kernel/db、bootstrap、cmd/cli 分别为 2.217s、19.771s、9.859s，均通过。环境模板不含凭据，真实测试连接仅保留在被 Git 忽略的 `.env`。
- 当前基线：五个认证接口、会话族持久撤销、菜单聚合、最小幂等种子与认证 OpenAPI 均可运行。`000001`–`000003` 已发布，不编辑这些迁移。
- 下一步里程碑：M1b，仅交接，不在本次发布中开始实现。复用 `identity.JWTAuth`、`identity.ProfileService`、`identity/permission/registry.go`、`kernel/db.ScopedQuery`、`bootstrap.SeedIdentity`、`cmd/cli seed` 和 `api/openapi/identity.yaml`。

### 下一位必读文件（按顺序）

1. `AGENTS.md` → `docs/04-conventions.md` §2、§3.2、§4、§5。
2. `docs/milestones/M1b.md` → `docs/milestones/M1.md` §7–§12 → `docs/milestones/M1a.md` §5/§6 与本交接段。
3. `docs/03-data-model.md` §2 → `docs/01-PRD.md` EPIC-1 → `docs/ADR.md` ADR-011/012/013。
4. M1a 的 `internal/modules/identity/`、`internal/bootstrap/`、`internal/kernel/{db,cache,audit}/` 及相应测试，重点看 `SessionRevoked`、ScopedQuery、认证查询 DryRun 矩阵和种子不变式。
5. `.golangci.yml` → `Makefile` → `cmd/cli/main.go` → `api/openapi/identity.yaml`；复跑 `make lint`、`make test`、`make test-integration`，再进入 M1b 任务。

## M1b1 · 管理 CRUD、registry 与种子语义（2026-10-31）

- T1.5–T1.9 本地验收通过，交付停在 M1b1；M1b2 待实现。
- 18 个管理 operation：用户 CRUD/密码重置/专用角色分配、角色 CRUD/专用菜单权限部门授权、菜单 CRUD/移动、按 module 分组的权限列表；写入支持可选 Idempotency-Key，用户 PUT 拒绝关联字段。
- 删除遵循 ADR-014 第 9 条：用户软删、内置 admin 保护；角色/菜单物理删前检查所有引用，返回 20105 或带 children/roles 计数的 10004；super_admin 保护；删除与整行 before/JSON null after 审计同事务，审计失败回滚。
- 菜单与 seed 共用租户事务 advisory lock；锁内重读包括隐藏/禁用节点的完整图，校验环和全子树深度；两个交叉事务实测一个成功、一个 20106，最终无环。
- registry 含 20 个当前/兼容权限码；`go run ./cmd/cli permissions export` 确定性导出；路由全部绑定 registry 常量；OpenAPI 与 18 个管理/5 个认证 operation 双向比对，管理响应也逐条验证 schema。
- seed 仅新建角色写默认授权；已有角色不补权，重跑不恢复已撤销的权限/菜单/用户角色，不重置密码。角色页面加入本期系统菜单，M1a 兼容权限与角色保留。
- 验收：make lint 零告警；make test 的 17 个测试包通过；最终 make test-integration 的 kernel/db、bootstrap、cmd/cli 分别为 2.299s、35.136s、9.615s。迁移回归在隔离 schema 中完成 up→3、down→0、up→3。完整逐项证据见 M1b §10。
- 自查：39 个相关 Go 文件的文件/函数长度检查 0 违规，本期函数复杂度 ≤12；internal 无 TODO/not implemented；用户审计与响应不含凭据，配置硬删保留完整行；无迁移或依赖变更。
- M1b2 入口：`identity.AdminRoutes`/`RequirePerm`、`repo.Admin` 与 bootstrap 管理适配器、registry 导出和管理 OpenAPI。继续 T1.10 查询声明/数据权限矩阵、T1.11 版本化失效、T1.12 会话管理、T1.13 回归；本次未开始这些任务。

## M1b2 · 数据权限、版本化失效、会话管理（2026-10-31）

- T1.10–T1.13 已实现并完成本地自动验收，停在人工验收；分支 `codex/m1b2-data-scope-sessions`。未提交、合并或发布；开工前 M1b1 及其他文档改动保留。
- 查询声明与受限门面落位五种 scope；用户/会话按 owner、部门并集过滤，缺身份/无有效角色/未声明拒绝；配置表按 tenant + RequirePerm。INSERT/改归属显式校验；list/count 同一基础关系、根实体去重。
- Reflection：Admin 26=26、Sessions 2=2、Auth 11=11、Profile 3=3、Seed 6=6，合计 48=48。详情 Scope、count 条件、子查询 tenant 三种实际临时变异均被 SQL 矩阵拦截并逐字节恢复；恢复后矩阵通过。
- 权限/菜单共享无 TTL 版本键；payload 有版本与 5 分钟 TTL，缺键新代不命中旧残留；旧慢读者只回填旧代。提交后去重失效，500/批、最多两个执行单元、逐键一次预算内重试；故障为 2xx + pending、指标和 Warn，TTL 收敛实测通过。
- 会话只列仍有有效 refresh 的未撤销 family；强踢复用 refresh 同一锁，已知/重复/过期历史 204、未知/跨租户/跨 owner 404。Redis 写失败 503 保留 DB 撤销，清空 Redis 后仍拒绝 access/refresh；其他 sid 不受影响。
- OpenAPI 与实际路由：20 管理 + 5 认证 operation 双向一致，补会话与 pending header/20108；M1a/M1b1 回归全过。
- 时延：真实 PostgreSQL，16 个并发冷读者；200 用户 CI（miniredis）P50/P95/max=145.0215/229.3186/241.1129ms；独立 2000 用户（真实 Redis）=1.0282144/1.9200862/2.0139199s，均计入 commit→同步失效→权限与菜单冷读回填的时间，最大 <5s。独立命令 `make test-authz-benchmark`（authzbench tag）。
- 验收：make lint 零告警；make test 19 个测试包通过；make test-integration（0 skipped）db/bootstrap/cli=2.052/55.351/23.306s；本期相关 race 检查通过（bootstrap 124.926s）；隔离 schema 迁移 up/down/up 回归通过。
- 自查：56 个相关 Go 文件长度/函数长度 0 违规，本期函数复杂度 ≤12；无 TODO、故障 helper、敏感日志、依赖/迁移/Go 版本变更。详细命令、负向输出、时延表、测试映射与限制见 `docs/milestones/M1b.md` §11。
- 偏差：无；保留 ADR-014 已接受的故障期 TTL 收敛限制，outbox 仍属 M3。下一步仅 M1b2 人工验收，未开启 M2。

## M2 · 店铺与凭据（2026-11-01）

- 状态：完成，T2.8 权限增量发布、OpenAPI 收尾与全链路回归已验收；按要求停在验收阶段，未合并、未打 tag。
- 完成项：shop/credential/file 能力、确定性权限导出、一次性 `permissions grant --role --codes --release` 事务发布、不可变 release marker 与审计、提交后 authz 失效去重、13 个 M2 管理端点 OpenAPI 录入与路由双向校验、seed 已有角色不补权不变式。
- Tag：待人工发布（建议沿用仓库 M2 命名约定）；本次不创建或推送 tag。
- 遗留：race 覆盖由 CI 执行；Redis 超时仍为现有固定值，后续配置化。

## M4a · 库存、类目映射与验收交接（2026-11-04）

- 本轮覆盖 REQ-PRD-003 / 004；分支 `feat/M4a-catalog`。代码与本地自动验证完成，停在 M4a 人工验收；M4b 未开始，未提交、推送、合并或打 tag。
- 库存：每个成功写入的 listing_sku 在同步页事务内追加快照，available 取报告 stock，reserved 未报告为 0；synced_at 取本次同步 UTC 时间。来源仲裁跳过不重复记快照，失败页整体回滚。
- worker leader 启动补清，并按 UTC 日清理严格早于 30 天的数据，失败重试；GET `/inventories` 复用 tenant / shop scope。商品详情 reported_stock 汇总全部关联 listing_sku，标注「可能重复计算」。
- 类目：GET 池支持 matched=false / true / all 及可选 platform_id；PUT 人工指定 source=manual，支持可选 Idempotency-Key；POST auto 仅返回规则候选，同步与 auto 均不写候选、不覆盖 manual。商品 category_id 筛选实时关联已应用映射。
- OpenAPI：本轮四个映射 / 库存 operation 的参数、请求与响应 schema、权限和错误说明已补齐，并由实际 HTTP 与路由清单验证；商品详情和类目筛选口径同步记录。
- 验收：`./scripts/verify.ps1 -Integration -Migration` 退出 0，最后一行 `all checks passed`；集成 db/bootstrap/cli 为 3.878s / 166.671s / 8.836s；迁移 `7 → 6 → 7` 均 dirty=false，精确断言含 export_job 的 8 表消失 / 恢复。
- 十万商品测试：25 次首页 service 调用 P95=92.5994ms，page=100 后游标 5 页无重复；三条 listing 报告库存和随同步从 3 更新为 4。
- 自查：20 个相关 Go 文件文件 / 函数长度 0 违规；internal 无 TODO / not implemented；diff --check 通过；无新增日志、依赖或既有迁移改动。缓存均位于系统临时目录。
- 证据边界：本机无 make，使用 verify 等价目标；race 仍待 CI。M4 T4.10 的 EXPLAIN / 服务端慢查询日志尚无现场证据；单条 resync、内部类目写接口未包含本轮，未记作完成。历史重放执行器与异步导出按 M4 拆分留给 M4b。
- 交接入口：`docs/milestones/M4a-acceptance.md`（逐项证据与限制）→ `docs/milestones/M4.md` → `api/openapi/catalog.yaml` → `internal/modules/catalog/{listing,inventory,http,category_sync}.go` → `internal/bootstrap/catalog_{inventory,category,category_mutation,product_read}.go`；按要求仅等待 M4a 验收。

## M8 · 开放网关收尾（2026-11-10）

- 状态：M8a/M8b 已落地；M8c 文档、演练、压测与本地验收收尾完成，分支 `feat/M8c-gateway-hardening`，基线 `5a9bbaf`。M8 到此停止，不开启 M9；本批未提交、推送、合并或发布标签。
- 当前公开能力：`GET /open/v1/orders/{public_id}/status`、本应用真实 usage、固定 503 的 balance。独立 HMAC/DTO/OpenAPI、逐订单应用上限与 owner 当前范围、主库即时吊销、服务端 call_id、持久准入/日志/成功计量、Redis 分层桶和 UTC 日/月配额；M8 只计量不扣费。
- 接入文档：`docs/gateway/` 覆盖六段签名、UTF-8/QueryEscape、时间窗/nonce、错误映射/重试与轮换/泄露手册。bash/curl 和 Go SDK 风格示例均在真实 open 栈验签通过，secret 不进入日志或示例夹具。
- 三个可执行演练 `scripts/gateway/{rotation,revocation,quota}.ps1` 全部 exit=0；分别验证窗口内双 key 与窗口后失效、吊销后第一次请求 401、日/月 N+1 返回 429/limit_type/UTC Retry-After 且不超发。各自执行 S3 全部 15 步，耗时 34.69s / 30.35s / 31.25s；清理 schema/Redis/自有进程通过。
- S3 第 15 步：真实管理授权 → 独立签名 → 200 → 根据 call_id 核对一条日志和 meter → 执行 bash/Go 两示例 → usage 恰好三次成功/计量，零 processing。原清理仍为第 14 步，执行顺序 1～13、15、14。
- 失败清理复验：`smoke.ps1 -Fault SkipRevoke` 如期在第 13 步 FAIL/exit=13，第 15 步 SKIPPED，第 14 步清理 PASS，schema/Redis 均无残留；未改变产品或放宽断言。
- 压测：真实 PostgreSQL/Redis，业务读在计时外取得相同真实快照；2000 次/档、1/4/16/32 并发。单并发配对额外 p50/p95/p99=40.961/52.757/58.560ms，**20ms 目标未通过**；最高观测 open=123.47 QPS、零错误。计量 DB 阶段 p99=24.062ms，主瓶颈指向持久配额/计量及主库生命周期写入；SQL/锁等待/池等待细分及优化方案留待评审，不放松吊销或持久化。容量入口 build tag + 显式环境开关，普通 PR CI 不跑容量/统计判定。
- 验收：`powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1 -Integration -Migration` exit=0，最后一行 `all checks passed`；db/bootstrap/cli 全集成为 5.468s / 206.517s / 10.563s；迁移 18→17→18 均 dirty=false。普通与 gatewaybench tag lint 通过；分片对账 `Source 317 = shards 316 + excluded 1`，Gateway 25 个集成测试恰好一个分片。
- 自检：6 个 Go 文件最大 193 行、函数最大 60 行；6 个 PowerShell 文件 parser/ASCII、bash 语法、gofmt、internal 空实现与敏感输出扫描、diff whitespace 通过。缓存留在系统临时目录，无依赖、生产实现或既有迁移变更。race 仍需合并前 CI 证明。
- 原 PRD 遗留：任意 HTTP 代理/SSRF/熔断与完整上游保护、管理端日志检索、公开 routes、逐路由策略覆盖、完整字节观测未交付；REQ-GW-001/007/008 未按原 PRD 全范围完成，不以本批脚本替代实现或自动转入 M9。
- 交付入口：`docs/milestones/M8c-acceptance.md` 逐项需求矩阵及证据、`M8c-performance.md/json` 性能与边界、`docs/gateway/README.md` 接入、`scripts/gateway/` 演练与容量入口、`scripts/smoke/gateway.ps1` 第 15 步。

## M9 · 计费、账单与容量收尾（2026-11-10）

- 范围：M9a账本钱包/预扣恢复、M9b权益/套餐/三方对账、M9c账单/调整/容量与文档。M9c分支 `feat/M9c-statement-capacity`，基线 `cffa3f3`；只新增21/22号迁移，无新依赖。
- 账单：UTC自然月、整数分；从独立财务事实生成draft，金额等于正负明细求和，权益用量/套餐购买分列且不计充值。issued头/明细在服务和数据库两层冻结，迟到、退款、补扣进入后续开放月并引用原交易/原账单。
- 调整与管理：退款累计不超原settle；补扣不足保持open，充值后用原凭据重试且仅扣一次。钱包流水、差异处置凭据、resolved及审计同事务。列表/详情/生成/发行/调整均具备JWT/RBAC/owner范围和写幂等；OpenAPI共18个操作。
- 保留：财务凭据至少三年，未决应用事实长期受保护；draft不清理。候选检查不授权物理删账；超过三年的归档/恢复RPO留M10裁决。
- 性能按已批准A落地：意图/processing日志/charge/日月配额/资金冻结或权益预留合并成一次PG业务往返；原有持久事实全部保留；Touch改为同事务CTE；应用/路由/密钥按版本缓存5秒，撤销主动失效并在每次命中核验主库版本/状态，跨实例撤销及故障拒绝有回归。配额最终依据仍为PG，未实施B；对账继续既有有界分页。
- 同脚本前后实测：现金并发1 open p99 76.544→45.878ms，权益95.974→41.153ms；配对额外p99分别75.050→42.555ms、64.429→39.800ms。固定50QPS p99分别102.468→54.198ms、95.249→53.793ms；errors=0，核对每场景2620调用/计量/收费/配额。达到本机85/100/150ms暂定目标，未触发>100ms停止条件；不是生产持续容量承诺。
- S3扩为16步：真实签名v2调用→扣5分→对账零差异→生成5分draft，明细下钻原call；16 PASS、49.38s、exit=0，schema/Redis/自有进程清理。原15步断言与可选演练保留。
- 验收：`verify.ps1 -Integration -Migration`退出0，最后一行`all checks passed`；全量bootstrap 244.045s，集成db/bootstrap/cli为6.317/251.154/11.408s；迁移22→21→22均dirty=false。普通/tag lint通过；分片365=364+1；32个Go文件/93个新增修改函数尺寸违规0。验收证据入口：[M9c逐项验收](milestones/M9c-acceptance.md)、[容量报告及before/after JSON](milestones/M9c-performance.md)、[账单接口契约](milestones/M9c-contract.md)；部署/运维命令在README。
- 原PRD尚未完成：REQ-BILL-004按route维度的日报（现为app/currency/服务日）、REQ-BILL-007低余额阈值通知。余额不足402/90102、非负余额和fail closed已实现，但不能替代低余额通知；本批不改M9b既有模型或扩展通知架构。
- 验证/容量遗留：CI race（本机CGO=0，无gcc）、生产Linux与多实例/多钱包复测、30分钟稳定段/72小时长跑、对账并跑、PG锁/池/WAL与长期行数/分区/备份容量，以及已响应扣款允许丢失多少的RPO人裁决。月度自动生成/发行当前未接定时器；管理手动出账可用。
- 已批准不做：在线支付、发票、退款审批、套餐剩余单位自动退款、自动续费、FX、阶梯价；不记作完成，不在收尾自行新增。
- 继承全部已知历史遗留：backlog #1 lint v2迁移、#3 identity既有93行函数、#4 worker HTTP就绪、#5 readyz运维鉴权、#6 分享撤销404/403文档对齐（#2已解决）；M4a EXPLAIN/慢查询现场证据与单条resync/内部类目写入口；M8完整HTTP代理/SSRF/熔断、日志检索、公开routes、逐路由策略、字节观测，详见各原验收文档。本批不顺手修复。
- 交付后停止并进入收尾复盘；不自动开启M10、优化B或发布动作。未推送、建PR、合并或打tag。
