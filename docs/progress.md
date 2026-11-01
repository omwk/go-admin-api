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
