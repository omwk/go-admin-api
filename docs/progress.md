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
