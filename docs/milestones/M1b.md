# M1b · RBAC、管理 CRUD 与数据权限

- 状态：待实现；依赖 M1a 独立验收、合并并打 **`v0.M1a`**。
- 共享规格：[M1.md](M1.md)，ADR-011/012/013。
- 分支：`codex/M1b-identity-rbac`。
- 预计：约 5 人日；覆盖共享卡 T1.5–T1.10。
- 覆盖需求：REQ-IAM-003–010 的剩余管理、授权、数据范围与审计行为；保持 M1a 的认证/菜单契约。
- 完成标志：无权限返回 403/20107，数据行隔离，按 sid 强踢，Redis 正常时菜单/鉴权变更 5 秒内生效。

## 1. 必读

1. `AGENTS.md`。
2. `docs/04-conventions.md` §2、§3.2、§4、§5。
3. 本卡、`docs/milestones/M1.md` §7–§12、M1a 的验收与交接。
4. `docs/03-data-model.md` §2、`docs/01-PRD.md` EPIC-1、`docs/ADR.md` ADR-011/012/013。
5. M1a 实际产出的 identity、ScopedQuery、cache、audit、bootstrap、seed 与认证 OpenAPI；`.golangci.yml`。

## 2. 范围与不做

- 用户 CRUD/启停/重置密码/角色分配，角色 CRUD/菜单及权限授权，菜单 CRUD/树校验，权限列表，会话列表和按 sid 强制下线。
- 权限码固定 `module:resource:action`，首段必须等于 `sys_permission.module`；registry、种子、RequirePerm 和文档一致，不保存通配符或接口表动作缩写。
- 菜单/权限分表，配置类小表及其关联表物理外键；业务大表不建物理外键，不追改已合并迁移。
- 显式 Scope + depguard + DryRun；不做 GORM 全局数据范围注入，不自研 AST。`is_super` 只跳过 RBAC，数据范围由角色决定。
- 版本键无 TTL，菜单/权限缓存 TTL 5 分钟；DB 提交后对受影响用户同步 INCR。角色权限/菜单、用户角色、角色状态/数据范围、菜单配置变更均覆盖。
- M1 不做 outbox 失效重试，交 M3 T3.11；不做前端代码、推送、M2 店铺 API 或其他业务里程碑。

## 3. 允许改动的实现文件

- `internal/modules/identity/`、`internal/bootstrap/`：管理接口、鉴权/数据范围、authz 缓存、审计与测试，复用 M1a 能力。
- `internal/kernel/{db,cache,audit}/`、`.golangci.yml`：补齐所需门面和 depguard，保留 M0/M1a 已验收行为。
- `cmd/`、`Makefile`：扩展既有幂等种子；`api/openapi/identity.yaml`：补管理接口契约。
- 本卡与 `docs/progress.md`：验收事实。不得编辑已合并的 000001/000002 迁移；超出已定模型的结构变更按 AGENTS.md 发起定向评审。

## 4. 接口

完整路径与权限见共享卡 M1 §8；本卡只新增以下管理接口，必须带 JWT + LoadIdentity + RequirePerm，并在 repo 执行适用的数据范围：

| 资源 | 方法与路径 | 权限 |
|---|---|---|
| 用户 | `GET/POST /api/v1/admin/users`；`PUT/DELETE /users/{id}`；`POST /users/{id}/reset-password` | `system:user:list/create/update/delete/reset`（按方法取单个完整码） |
| 角色 | `GET/POST /api/v1/admin/roles`；`PUT/DELETE /roles/{id}`；`PUT /roles/{id}/menus`、`/permissions` | `system:role:list/create/update/delete/grant`（按方法取单个完整码） |
| 菜单 | `GET/POST /api/v1/admin/menus`；`PUT/DELETE /menus/{id}` | `system:menu:list/create/update/delete`（按方法取单个完整码） |
| 权限点 | `GET /api/v1/admin/permissions` | `system:menu:list` |
| 会话 | `GET /api/v1/admin/sessions`；`DELETE /sessions/{sid}` | `system:session:list`、`system:session:kick` |

表中省略前缀的子路径均位于 `/api/v1/admin` 下，实际鉴权码以共享接口表的动作逐一展开。

## 5. 验收与交接

| 项 | 自动化/命令证据 |
|---|---|
| 管理 CRUD | happy/error path；停用立即拒绝；被引用角色删除 20105；菜单成环 20106；外键防止悬空引用 |
| 权限码 | 三段格式和首段/module 一致；无权限为 403/20107；种子/鉴权声明引用 registry |
| 数据范围 | 按 repo 方法列 DryRun 矩阵：列表、详情、计数、更新/删除及 join/alias；reflection 校验接口方法登记无遗漏；五种 scope、tenant 条件、缺失身份拒绝路径齐全；跨 owner 集成测试 |
| depguard | 业务 repo 直接导入 GORM/kernel DB/SQL 驱动会失败；合法 ScopedQuery 使用通过；无自研 AST |
| 超管 | `is_super` 仍走数据范围；种子 `super_admin.data_scope=all` 不变式及审计覆盖 |
| 会话 | 按 sid 聚合，强踢下一请求失效；只撤目标 family，不影响同用户其他设备或管理员自身会话 |
| 缓存正常 | DB 提交后同步 INCR；权限和菜单共享版本，5 秒内鉴权及下一次 `/auth/menus` 查询反映变更；并发回填旧版本不能复活旧权限 |
| 缓存故障 | INCR 失败有安全 Warn 和指标；Redis 不可用拒绝服务；恢复后遗留缓存按 5 分钟 TTL 淘汰；不要求 M1 实现 outbox |
| 独立交付 | M1a 全部回归保持通过；完整 seed 幂等且不重置密码；OpenAPI 与共享接口表一致 |
| CI | `make lint`、`make test`；有数据库时 `make test-integration`，未提供连接时据实说明 |

M1 阶段用本卡内测试数据验证 owner 隔离，不提前实现 M2 店铺接口；M2 对真实店铺表/API 再做纵向验收。按 AGENTS.md DoD 给证据，合并后打 `v0.M1b`；M3 接手 outbox 失效重试，M10 接手 bcrypt cost 压测裁定。
