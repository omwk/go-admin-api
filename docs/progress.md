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
- 实现与本地验收日期：2026-10-31；覆盖 T1.0–T1.4，待最终 CI 确认与验收合并；尚未创建 v0.M1a。
- 前置查询优化：SessionRevoked 存在性 SQL、000003 撤销部分索引、50 条历史记录下 SQL 条数保持 1；迁移并发锁使用非阻塞探测，避免 concurrent index 的旧快照死锁。
- 前置 CI：[73e2905](https://github.com/omwk/go-admin-api/actions/runs/41130424607)、[02a8e09](https://github.com/omwk/go-admin-api/actions/runs/41132295629) 均成功，包含 go test -race ./...；密码比对移到事务外，事务内锁行重读核验。
- T1.4：me/menus 同源 user 和 permissions，菜单与角色菜单关联各查询一次；排序/隐藏/禁用/button/父隐藏/深度规则均有自动测试。
- seed：权限→菜单→角色及角色授权→部门→admin 与用户角色关联；连跑两次行数和密码哈希不变；super_admin 的 data_scope=all 有不变式测试。
- OpenAPI：五个认证端点、请求/响应及 20109/503 语义；实际 HTTP 响应契约测试通过。
- 本地命令：make lint、make test、make test-integration 均通过；集成测试包含真实 PostgreSQL 隔离 schema、Redis 测试服务器、实际 CLI/make 目标与 API 进程。
- 工具链：GNU Make 4.4.1、GCC、golangci-lint 已安装；未升级项目 Go 版本或新增 Go 依赖。
- 交接入口：identity.JWTAuth、identity.ProfileService、identity/permission/registry.go、bootstrap.SeedIdentity、cmd/cli seed、api/openapi/identity.yaml。
- 偏差：前置索引使用新增 000003，不编辑已提交迁移；补齐 race 下的登录短事务；M1b 未开始。
