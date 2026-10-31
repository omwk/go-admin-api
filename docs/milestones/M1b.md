# M1b · RBAC、管理 CRUD、数据权限与版本化失效

- 状态：M1b1 本地验收通过；M1b2 实现及本地自动验收完成，停在人工验收。Gate A 裁决见 `docs/ADR.md` **ADR-014**（2026-10-31）。
- 依赖：M1a 已合并并打 `v0.M1a`。
- 共享规格：[M1.md](M1.md)、ADR-011/012/013/014。
- 分支：`feat/M1b-identity-rbac`（两个批次各自 PR）。
- 预计：**7–10 人日**（评审后上调，原估 5 人日）。
- 覆盖需求：REQ-IAM-003 ~ 010 的剩余管理、授权、数据范围与审计行为；保持 M1a 的认证/菜单契约。
- 完成标志：无权限 403/20107；数据行隔离；按 sid 强踢；权限变更 5 秒内生效（Redis 正常）；重跑种子不复活被撤授权。

**拆两批，各自验收、各自合并**：

| 批次 | 内容 | 预计 | Tag |
|---|---|---|---|
| **M1b1** | T1.5–T1.9：管理 CRUD + 权限 registry 导出 + 种子语义 | 4–5 人日 | `v0.M1b1` |
| **M1b2** | T1.10–T1.13：数据权限矩阵 + 版本化失效 + 会话管理 | 4–5 人日 | `v0.M1b` |

> 先做 M1b1 的原因：CRUD 与种子是「管理面能不能用」，M1b2 是「权限准不准」。后者风险高、需要前者提供的接口来造测试数据。

---

## 1. 必读

1. `AGENTS.md`、`docs/04-conventions.md` §2、§3.2、§4、§5。
2. 本卡、`docs/milestones/M1.md` §7–§12。
3. `docs/ADR.md` **ADR-014**（本次裁决）与 ADR-011/012/013。
4. `docs/proposals/M1b-review.md`（评审原文，理解取舍）。
5. M1a 产出：`identity/`、`bootstrap/`、`kernel/db/scoped.go`、`kernel/cache`、`kernel/audit`、`permission/registry.go`、seed、`api/openapi/identity.yaml`。

## 2. 范围与不做

**做**：

- 用户 CRUD/启停/重置密码/角色分配；角色 CRUD/菜单与权限授权/自定义部门；菜单 CRUD 与移动；权限点列表；会话列表与按 sid 强制下线。
- 权限码三段式，首段等于 `sys_permission.module`；registry 为唯一词典，种子与 `RequirePerm` 都从它取。
- 菜单/权限分表 + 配置类小表物理外键；业务大表不建外键。
- 显式 Scope + 查询声明 + depguard + DryRun 矩阵；不做 GORM 全局注入，不自研 AST。
- 版本键 `authz:ver:{tenant}:{user}` + `authz:perm|menu:{tenant}:{user}:v{n}`，TTL 5 分钟。
- 种子的「新建 vs 已有」语义见 ADR-014 第 6 条。

**不做**：

- outbox 失效重试（M3 T3.11）、前端代码、M2 店铺 API、任何业务里程碑。
- 新增 session 表、菜单物化路径列、user/role/menu 的 version 列（ADR-014 第 7 条例外）。
- 预置 M2–M9 的未来权限码。
- 删除/重命名权限码（需要单独迁移计划）。

## 3. 允许改动

- `internal/modules/identity/`、`internal/bootstrap/`：管理接口、鉴权/数据范围、authz 缓存、审计与测试。
- `internal/kernel/{db,cache,audit}/`、`.golangci.yml`：补齐门面与 depguard，保留 M0/M1a 已验收行为。
- `cmd/`、`Makefile`：扩展幂等种子；`api/openapi/identity.yaml`：补管理接口契约。
- 本卡与 `docs/progress.md`：验收事实。
- **禁止**编辑已合并的 `000001/000002/000003` 迁移；本卡不需要新迁移（ADR-014 第 7 条）。

## 4. 接口

前缀 `/api/v1/admin`。鉴权码按方法取**单个完整码**（不是 `a/b` 缩写）。

| 资源 | 方法与路径 | 权限 |
|---|---|---|
| 用户列表 | `GET /users` | `system:user:list` |
| 创建用户 | `POST /users` | `system:user:create` |
| 更新用户标量 | `PUT /users/{id}` | `system:user:update` |
| 软删用户 | `DELETE /users/{id}` | `system:user:delete` |
| 重置密码 | `POST /users/{id}/reset-password` | `system:user:reset` |
| 用户角色 | `PUT /users/{id}/roles` | `system:user:grant` |
| 角色列表 | `GET /roles` | `system:role:list` |
| 创建角色 | `POST /roles` | `system:role:create` |
| 更新角色标量 | `PUT /roles/{id}` | `system:role:update` |
| 物理删角色 | `DELETE /roles/{id}` | `system:role:delete` |
| 角色菜单授权 | `PUT /roles/{id}/menus` | `system:role:grant` |
| 角色权限授权 | `PUT /roles/{id}/permissions` | `system:role:grant` |
| 角色自定义部门 | `PUT /roles/{id}/depts` | `system:role:grant` |
| 菜单配置树 | `GET /menus` | `system:menu:list` |
| 创建菜单 | `POST /menus` | `system:menu:create` |
| 更新/移动菜单 | `PUT /menus/{id}` | `system:menu:update` |
| 物理删菜单 | `DELETE /menus/{id}` | `system:menu:delete` |
| 权限点 | `GET /permissions` | `system:menu:list` |
| 会话列表（M1b2） | `GET /sessions` | `system:session:list` |
| 强踢会话（M1b2） | `DELETE /sessions/{sid}` | `system:session:kick` |

> `system:user:grant` 是新增权限码，必须先进 registry 与种子。

## 5. 裁决摘要（详见 ADR-014，不要再改）

| 议题 | 结论 |
|---|---|
| 数据权限 | 查询声明 + 受限门面；未登记/缺身份直接拒绝；多角色 scope 取并集 |
| 失效 | 提交后 pipeline 分批 INCR（500/批）；失败返回 2xx + `X-Authz-Invalidation: pending`，不返回 503 |
| 强踢 | 复用全族历史；已知重复踢 204，未知 404；不加 session 表 |
| 权限码 | registry 唯一词典；OpenAPI 带 `x-permission`；CI 比对方法+路径→码 |
| 菜单移动 | 邻接表 + 租户树锁 + 整棵子树深度校验；不改 path 语义 |
| 种子 | 只补本期 system；重跑不复活被撤授权 |
| 版本列 | 例外：不加 version；更新必须检查 `RowsAffected`，为 0 返回 404 |
| 授权端点 | 专用子路径，不用大 PUT 承载关联字段 |
| 删除语义 | 用户软删；角色/菜单物理删，先检查引用并返回 409；零行返回 404；保护 super_admin/admin；硬删整行写审计 before，after 为 JSON null（ADR-014 第 9 条） |

## 6. 任务拆分

### M1b1（4–5 人日）

| # | 任务 | 产出 | 验收 |
|---|---|---|---|
| T1.5 | 用户 CRUD + 角色分配 | service/handler/repo | 停用立即拒绝；`PUT /users/{id}` 不改变角色；`/roles` 子资源单独生效 |
| T1.6 | 角色 CRUD + 授权 | service/handler | 删除被引用角色 20105；授权后 5 秒内生效（M1b2 验证） |
| T1.7 | 菜单 CRUD + 移动 | service/handler | 成环 20106；超深 10001；异租户父 404；并发交叉移动不成环 |
| T1.8 | 权限 registry 导出 + 路由绑定 | `permission/`、路由声明、导出器 | 未注册码注册失败；CI 比对路由与 OpenAPI 一致 |
| T1.9 | 种子语义 | `cmd/cli seed` | 重跑不复活被撤授权；不重置密码；已有角色不自动补权 |

### M1b2（4–5 人日）

| # | 任务 | 产出 | 验收 |
|---|---|---|---|
| T1.10 | 数据权限矩阵 | `scope` 声明、DryRun 矩阵、跨 owner 集成测试 | 见 §7 第 1–3 行 |
| T1.11 | 版本化失效 | `authz` 包、批量 INCR、回填版本校验 | 5 秒内生效；旧版本回填不复活；失败指标 |
| T1.12 | 会话管理 | 列表 + 按 sid 强踢 | 204/404 语义；不动其他 sid |
| T1.13 | OpenAPI + 回归 | `api/openapi/identity.yaml` | 管理端点齐全，认证端点仍是 5 个 |

## 7. 验收（必须自动化）

### M1b1

| # | 项 | 断言 |
|---|---|---|
| 1 | CRUD | happy/error 路径；被引用角色 `20105`；外键防悬空 |
| 2 | 更新语义 | `RowsAffected=0` → 404；不存在 → 404；跨租户 → 404 |
| 3 | 菜单移动 | 自身/子孙成环 20106；整棵子树变超深 10001；隐藏父也参与校验 |
| 4 | 并发移动 | A→B 与 B→A 并发，最终无环（测试用两个事务交叉） |
| 5 | 权限码 | 插两段码被拒；registry 未注册的码注册路由失败 |
| 6 | 契约 | 路由集合 ↔ OpenAPI operation ↔ `x-permission` 三者一一对应 |
| 7 | 种子 | 连跑两次：行数稳定、密码不变；手工撤掉默认授权后重跑，仍保持撤销 |

### M1b2

| # | 项 | 断言 |
|---|---|---|
| 1 | scope 覆盖 | reflection 枚举 repo 接口方法与 DryRun 登记集合**完全相等**；多一个或少一个都失败 |
| 2 | 五范围 | 列表/详情/count/更新/删除 × `all/dept/dept_and_child/self/custom`；跨 owner 读不到、改不动 |
| 3 | 负向验证 | 故意删掉某方法的 Scope、删掉 count 条件、漏子查询 tenant，矩阵必须报错（不提交故障版本） |
| 4 | 越权边界 | `is_super` + `self` 仍不能跨 owner；无有效角色 → 拒绝 |
| 5 | 失效时延 | 改角色权限 → 2000 用户场景，实测 commit 到冷读完成的时间，附 P50/P95/最大值 |
| 6 | 失效失败 | 注入 Redis 失败：响应 2xx + `X-Authz-Invalidation: pending` + 指标 +1；5 分钟内收敛 |
| 7 | 回填竞态 | 读版本 n 的慢读者不得把旧快照写进版本 n+1 |
| 8 | 强踢 | 已知 sid → 204，下一请求 401；重复 → 204；未知 → 404；同用户其他 sid 不受影响 |
| 9 | Redis 清空 | 刷掉全部 key，已踢 sid 仍被 DB 拒绝 |

## 8. 可粘贴的 Codex prompt（M1b1）

```
新会话开工。先读 AGENTS.md 与 docs/ADR.md 的 ADR-014，再读 docs/milestones/M1b.md。
本次只做 M1b1 的 T1.5–T1.9：用户/角色/菜单 CRUD、权限 registry 导出、种子语义。
数据权限矩阵、版本化失效、会话管理属于 M1b2，本次不做。

必读（按序）：
AGENTS.md → docs/04-conventions.md §2/§3.2/§5 → docs/milestones/M1b.md §2–§7
→ docs/ADR.md ADR-011/012/014 → docs/milestones/M1.md §8
→ M1a 产出的 identity/、bootstrap/、permission/registry.go、seed

范围：以 M1b.md §3 为准。不需要新迁移；不得编辑 000001–000003。

要求：
- 权限码一律单个完整三段码，路由声明引用 registry 常量，禁止字符串字面量散落。
- 更新一律检查 RowsAffected，为 0 返回 404，禁止把零行当成功。
- 菜单移动在租户树 advisory lock 内重读全图后校验，包括隐藏/禁用节点；成环 20106，超深 10001。
- 种子区分新建与已有角色：已有角色不自动补权，重跑不复活被撤授权。
- 角色分配与自定义部门走专用端点，PUT /users/{id} 只改标量字段。

验收按 M1b.md §7 的 M1b1 表逐条覆盖，附 make lint / make test / make test-integration 输出。
完成后停在 M1b1 验收，不要开始 M1b2。

遇到需要改表结构、改契约、加依赖时，按 AGENTS.md §5.2 停下提问。
```

---

## 9. 交接

M1b2 需要 M1b1 提供的：完整 registry、可创建测试数据的 CRUD、带 `x-permission` 的 OpenAPI。M1b1 合并后打 `v0.M1b1`，并在 `docs/progress.md` 记录「M1b2 入口」。

### M1b2 实施清单

规格：本卡 §5–§7、ADR-011/012/014、M1 §9.5/9.6。沿用当前未提交的 M1b1 成果，不增加依赖、迁移或 M2 内容。

- [x] T1.10：在 `identity/scope/` 登记实体、固定别名和归属；bootstrap 从有效角色建立并集 Scope，通过 kernel/db 受限门面应用。管理 INSERT/改部门额外校验目标；列表/count 复用基础关系。先补 reflection/DryRun，再实现并跑跨 owner 集成；删除 Scope/count/子查询 tenant 的临时变异必须失败并恢复。
- [x] T1.11：在 `identity/authz/` 与 `kernel/cache/` 实现版本 payload、缺键安全初始化、旧版本回填、500/批与最多两执行单元的逐键失效。管理事务内解析受影响用户，提交后去重 INCR；失败头、指标与安全日志。运行慢读者、Redis 故障、200 用户 CI 与独立 2000 用户 commit→冷读基准。
- [x] T1.12：在 repo/bootstrap 与 identity service/HTTP 增加会话列表、按 sid 强踢，复用认证会话锁、历史与提交后标记；验证重复/未知/其他 sid、刷新并发与 Redis 清空。
- [x] T1.13：补 `api/openapi/identity.yaml` 两个管理 operation；运行权限契约、M1a/M1b1 回归、`make lint`、`make test`、`make test-integration`，记录矩阵数量、负向证据、时延表及 DoD。

## 10. M1b1 实现与验收记录

实施顺序：registry/结构化路由与确定性导出 → 独立管理 repo 端口和 bootstrap 适配器 → 用户/角色/菜单 service 与 HTTP → seed 新建/已有语义 → OpenAPI 与逐项自动化验收。管理写事务复用租户锁；不新增迁移、依赖或 M1b2 接口。

- [x] T1.5：用户 CRUD、标量更新、专用角色授权、密码重置、内置账号保护。
- [x] T1.6：角色 CRUD、专用菜单/权限/部门授权、四类引用保护与完整删除审计。
- [x] T1.7：全图菜单校验、共享事务锁、引用保护与完整删除审计。
- [x] T1.8：registry 导出、常量路由绑定、实际路由与 OpenAPI 权限集合相等。
- [x] T1.9：已有角色不补授权、密码保持、撤权重跑不复活。
- [x] 验收：make lint / make test / make test-integration，七项 M1b1 自动化与删除裁决逐条覆盖。

验收日期：2026-10-31。测试使用真实 PostgreSQL 的隔离 schema 与 Redis 测试服务器；本地连接从被忽略的 `.env` 注入，未记录连接凭据。

| §7 项 | 自动化证据（`internal/bootstrap/`，另注明模块单测） |
|---|---|
| 1 CRUD | `TestAdminIntegrationUserCRUDAndDedicatedRoles`、`RoleGrantsAndUpdate`、`RoleDeletionChecksAllReferences`；`MissingCrossTenantAndZeroRows` 直接验证复合外键拒绝跨租户关联；`PermissionDeniedAndDisabledAccount` 验证 403 与停用后的下一请求 401 |
| 2 更新语义 | `TestAdminIntegrationMissingCrossTenantAndZeroRows` 对用户/角色/菜单逐一验证更新、删除的不存在/跨租户 404，并直接调用 store 覆盖零行写入 |
| 3 菜单移动 | 模块 `TestAdminMenuMoveValidatesEntireGraph`；集成 `TestAdminIntegrationMenuMovesValidateHiddenWholeSubtree`：自身/后代成环、隐藏禁用节点、整棵子树超深、移到根、path 不变 |
| 4 并发移动 | `TestAdminIntegrationCrossingTransactionsCannotCreateMenuCycle`：A 读图后，B 在另一事务发起同租户锁请求；A 提交后 B 重读，恰好一个成功、另一个 20106，最终图无环 |
| 5 权限码 | `TestAdminIntegrationMissingCrossTenantAndZeroRows` 的两段码种子写入被拒；`TestAdminRegistrationRejectsUnknownAndAbbreviatedCodes`；permission 包 `TestExportIsDeterministicAndRegistryIsUnique` |
| 6 契约 | `TestAdminRouteOpenAPIAndPermissionsAreExactlyEqual` 双向比对实际路由、声明和 OpenAPI：18 管理 operation + 5 认证 operation；所有管理 HTTP 测试还逐响应核验 schema |
| 7 种子 | `TestIdentitySeedIntegrationIsIdempotentAndPreservesPassword`；`TestSeedIntegrationDoesNotResurrectRevokedGrants` 保持权限/菜单/用户角色撤销；`TestSeedIntegrationExistingRoleDoesNotReceiveDefaultGrants` 验证升级前已存在角色不获得默认授权且标量不变 |

删除追加裁决另由 `TestAdminIntegrationRoleDeletionChecksAllReferences`、`MenuDeletionExplainsReference`、`PhysicalDeletionAuditsFullRows`、`AuditFailureRollsBackPhysicalDelete` 覆盖：四类角色引用、children/roles 精确计数、内置保护、整行快照与 JSON null、审计失败回滚；`TestPhysicalDeleteSnapshotPreservesInt64` 防止快照丢失大整数精度。`TestAdminDeletionConflictsAreDocumented` 核验两个 DELETE 的 409 文档。

```text
$ make lint
golangci-lint run
# exit 0，零告警

$ make test
go test ./...
ok  github.com/omwk/go-admin-api/cmd/cli                    23.449s
ok  github.com/omwk/go-admin-api/internal/bootstrap         39.770s
ok  github.com/omwk/go-admin-api/internal/modules/identity  13.120s
# 17 个测试包通过，其余包无测试；exit 0

$ make test-integration
go test -count=1 -v ./internal/kernel/db ./internal/bootstrap ./cmd/cli -run Integration
--- PASS: TestMigrateIntegrationReversibleAndRepeatable (0.60s)
ok  github.com/omwk/go-admin-api/internal/kernel/db  2.299s
ok  github.com/omwk/go-admin-api/internal/bootstrap  35.136s
ok  github.com/omwk/go-admin-api/cmd/cli             9.615s
# exit 0；CLI 内 make migrate-up、seed 两次、migrate-version 均通过

$ go run ./cmd/cli permissions export | ConvertFrom-Json | Measure-Object
Count: 20

$ rg -n 'TODO|not implemented' internal
# 无匹配（rg exit 1）

$ git diff --name-only -- migrations go.mod go.sum
# 无输出
```

迁移 up/down 由上述 `TestMigrateIntegrationReversibleAndRepeatable` 在隔离 schema 中验证：up→3、连续 down→2/1/0、再 up→3；不回滚开发数据库，不编辑 000001–000003。

日志检查命令：`rg -n 'record\(|audit\.Event|slog\.|logger\.|log\.Print|fmt\.Print' internal/modules/identity internal/bootstrap -g 'admin*.go' -g 'identity_admin*.go' -g '!**/*test.go'`。新增用户审计只记 ID/状态/部门与授权 ID，不记密码、哈希或联系方式；管理响应不含 password_hash，测试密码随机生成；角色/菜单硬删按批准要求记录整行。无新增调试输出。

本次 Go 文件长度检查（39 文件，0 违规；单文件 ≤400、单函数 ≤80）：

```powershell
$paths = @(Get-ChildItem internal/modules/identity/admin*.go,internal/modules/identity/repo/admin.go,internal/bootstrap/identity_admin*.go,internal/bootstrap/identity_seed*.go,internal/bootstrap/identity_openapi_test.go,cmd/cli/permissions_test.go,internal/modules/identity/permission/*.go,internal/kernel/audit/*.go,internal/modules/identity/model/model.go,internal/modules/identity/seed_catalog.go,internal/bootstrap/identity.go,cmd/cli/main.go)
$violations = 0
foreach ($file in $paths) {
    $lines = [IO.File]::ReadAllLines($file.FullName)
    if ($lines.Length -gt 400) { $violations++ }
    $start = -1
    for ($i=0; $i -lt $lines.Length; $i++) {
        if ($lines[$i] -match '^func ') { $start=$i }
        if ($start -ge 0 -and ($lines[$i] -eq '}' -or ($i -eq $start -and $lines[$i] -match '\}\s*$'))) {
            if ($i-$start+1 -gt 80) { $violations++ }
            $start=-1
        }
    }
}
"checked Go files=$($paths.Count); size violations=$violations"
```

补充使用临时 gocyclo 配置（`min-complexity: 13`）检查，本期新增及修改的管理、种子与契约校验函数均无 >12 的复杂度违规。

偏差说明：删除按已批准的 ADR-014 第 9 条执行，无未批准偏差。`sys_role` 无创建时间列，角色列表明确采用 `id desc`，可选排序字段见 OpenAPI；用户默认仍为 `created_at desc`。停止于 M1b1，本次未实现 T1.10–T1.13。

## 11. M1b2 实现与验收记录（2026-10-31）

覆盖 T1.10–T1.13，停止于 M1b2 人工验收；本次未创建提交、发布标签或启动 M2。工作分支 `codex/m1b2-data-scope-sessions`，保留开工时未提交的 M1b1 及文档改动。

### 实现与改动文件

- `internal/modules/identity/scope/{scope,queries}.go`：实体与 repo 方法声明，固定 alias/tenant/owner/dept 来源；未知声明与缺失身份拒绝。用户 owner 为自身 ID，会话 owner 为 user_id，角色/菜单/权限沿用 tenant + RequirePerm。
- `internal/bootstrap/identity_scope*.go`、`identity_admin_{store,reads,links,idempotency}.go`；`internal/kernel/db/scoped*.go`：有效角色并集、边界安全的部门子树、空 custom 空关系、INSERT/改部门目标校验、count/list 基础关系分叉和根 ID 去重。
- `internal/modules/identity/authz/`、`internal/kernel/cache/versions*.go`、`internal/bootstrap/identity_authz*.go`；`identity/profile.go`、`identity/admin{,_http}.go`：共享版本号、带版本 payload、5 分钟 TTL、缺键随机正整数新代且版本键无 TTL；回填只写读到的那一代。事务内收集影响用户，提交后去重、500/批、最多两个执行单元；逐键结果且预算内仅重试失败键一次。失败仍为 2xx + pending 头、Warn、指标。
- `identity/repo/sessions.go`、`identity/admin_sessions.go`、`bootstrap/identity_sessions*.go`、`identity.go`、`identity/admin_{routes,http_handlers}.go`：会话列表与强踢；复用 refresh 的事务 advisory lock、全族历史和提交后 Redis 标记。不缓存 sid 有效性。
- `api/openapi/identity.yaml`、管理契约测试、认证/种子 reflection 测试、`Makefile`：20 个管理 operation + 5 个认证 operation 双向相等；补 20108、pending 响应头及会话 204/404/503 契约；增加独立 `test-authz-benchmark`。

### Reflection 与矩阵

```text
$ go test ./internal/bootstrap -count=1 -v -run 'ReflectionDryRunMatrix|RepositoryDryRunMatrixMatches'
repo.Profile 方法数 = 3; DryRun 登记数 = 3
repo.Admin 方法数 = 26; DryRun 登记数 = 26
repo.Seed 方法数 = 6; DryRun 登记数 = 6
repo.Sessions 方法数 = 2; DryRun 登记数 = 2
repo.Auth 方法数 = 11; DryRun 登记数 = 11
PASS
ok  github.com/omwk/go-admin-api/internal/bootstrap  6.358s
```

合计 **48 = 48**，同时比较「接口方法」「DryRun 用例」「生产查询声明」三个集合。管理与会话各方法覆盖五种 scope；缺身份不发 SQL。计数与列表都检查 tenant、owner/dept 条件；部门和会话的关联子查询单独检查 tenant。关系、引用、幂等冲突等私有分支另由 `TestAdminDryRunPrivateRelationshipBranches` 覆盖，认证与种子沿用独立边界。

| §7 验收项 | 自动化证据 |
|---|---|
| 五操作 × 五范围、跨 owner | `TestScopeIntegrationFiveOperationsAndOwnerIsolation`：list/detail/count/update/delete；修改角色为 self 后 is_super 管理员只能列出自己，跨 owner 删除 404；无有效角色 403/20108 |
| 并集、边界与写入 | `TestScopeIntegrationUnionEmptyCustomAndWriteTargets`：空 custom、custom+self、all 吸收、空部门 path、INSERT self 越界和改归属拒绝；部门 `1.2` 不匹配 `1.20` |
| 一对多 | `TestScopeIntegrationOneToManyDeduplicatesRootUsers`：实际 JOIN 两个角色，用户 list/count 均按根 ID 去重 |
| 授权变化完整覆盖 | `TestAuthzIntegrationAllMutationSourcesSharePermissionAndMenuVersion`：权限、菜单授权、自定义部门、角色 scope/状态、菜单配置、用户角色全部改变同一版本 |
| 失效故障与 TTL | `TestAuthzIntegrationFailureReturnsCommittedSuccessAndConverges`：DB 已撤权，204 + pending，`authz_invalidate_failed_total` 精确 +1，5 分钟后冷读无旧权 |
| 回填与缺键 | `TestSlowReaderCannotFillNewGeneration`、`TestVersionedPayloadMissingVersionAndTTL`：用 channel 控制慢读者；旧代只能回填旧 key，缺版本不碰残存 v0/旧代，payload 版本不符不命中 |
| 批次、部分失败、预算 | `TestInvalidationDeduplicatesBatchesAndRetriesOnlyFailedKeys`、`TestVersionPipelineRecordsPartialFailure`：2000 唯一用户、重复输入、500/批、最多 2 worker、逐键成功/失败、取消后不重试 |
| fail closed 与账号状态 | `TestAuthzIntegrationCachedReadsFailClosedAndCheckCurrentAccount`：已预热缓存遇 Redis 故障仍 503；禁用账号不能靠缓存放行 |
| 会话语义与 Redis 清空 | `TestSessionsIntegrationListKickRepeatUnknownAndRedisFlush`、`FailureExpiredHistoryAndOwnerScope`：轮换不重复计数、不虚构 last_seen；已知/重复/过期历史 204，未知/异租户/跨 owner 404；其他 sid 有效，清 Redis 后 access/refresh 仍 401 |
| 强踢故障与刷新并发 | `TestSessionsIntegrationKickAndRefreshUseSameLock`：channel 确认 refresh 已持锁，kick 已发起同锁，再释放；新 refresh 也被 kick。Redis 写失败 503 后 DB 撤销保持 |

### 负向验证（实际临时修改，未保留故障代码）

```text
mutation=scope exit=1
FAIL TestAdminRepositoryReflectionDryRunMatrix/self/User
SQL does not contain "sys_user.id = 29"
restored=scope bytes_match=True

mutation=count exit=1
FAIL TestAdminRepositoryReflectionDryRunMatrix/self/Users
COUNT SQL does not contain "sys_user.id = 29"
restored=count bytes_match=True

mutation=subquery-tenant exit=1
FAIL TestAdminRepositoryReflectionDryRunMatrix/dept_and_child/Users
SQL does not contain "scope_dept.tenant_id = 17"
restored=subquery-tenant bytes_match=True
```

三个变异分别把详情查询降为只有 tenant、把 count 改为不带数据范围的关系、删除部门子查询 tenant 及对应参数。每次由 SQL 断言失败（非编译失败）证明矩阵拦截，`finally` 恢复原始字节；全部恢复后再次跑上述 reflection 矩阵通过。`rg -n 'm1b2Unfiltered' internal` 无匹配。

### Commit 到冷读完成时延

| 用户数 | Redis | 并发冷读者 | P50 | P95 | 最大 |
|---|---|---:|---:|---:|---:|
| 200（常规 CI 集成） | miniredis，真实 PostgreSQL | 16 | 145.0215 ms | 229.3186 ms | 241.1129 ms |
| 2000（独立 authzbench） | 真实 Redis，真实 PostgreSQL | 16 | 1.0282144 s | 1.9200862 s | 2.0139199 s |

测量从角色权限变更事务**已提交返回**开始，包含同步批量失效、读者排队、当前账号检查、权限和菜单的 DB 冷读、两份新 payload 回填。每用户一个完成样本；表中分位数是同一批所有用户的完成时间，非独立请求服务耗时。预热并确认旧权限后，经实际角色授权 HTTP 接口撤权；所有冷读确认旧权限消失，并断言整批最大值 <5s。

环境：本地 Windows/amd64，i7-10700K（8 核/16 线程），实际工具链 Go 1.25.4；仓库 `go 1.23` 未改。DB 池 10，独立基准 Redis 池 32；数据在隔离 schema 内，2000 个 Redis 用户键使用唯一 ID 并逐键清理，不清空外部 Redis。该结果只证明此约定负载/同步路径下的本地 SLA，不扩展到 Redis 故障场景。

```powershell
# TEST_DATABASE_URL 从被 Git 忽略的 .env 注入；不打印连接内容。
$env:TEST_REDIS_ADDR = '127.0.0.1:16379'
$env:TEST_REDIS_DB = '15'
make test-authz-benchmark
```

```text
go test -count=1 -tags authzbench -v ./internal/bootstrap -run '^TestAuthzIntegration2000UserBenchmark$'
commit→cold-read users=2000 readers=16 P50=1.0282144s P95=1.9200862s max=2.0139199s
--- PASS: TestAuthzIntegration2000UserBenchmark (25.45s)
ok  github.com/omwk/go-admin-api/internal/bootstrap  31.219s
```

### 验收命令与 DoD

```text
$ make lint
golangci-lint run
# exit 0，零告警

$ make test
go test ./...
ok  github.com/omwk/go-admin-api/cmd/cli                         16.879s
ok  github.com/omwk/go-admin-api/internal/bootstrap              57.425s
ok  github.com/omwk/go-admin-api/internal/modules/identity        17.013s
ok  github.com/omwk/go-admin-api/internal/modules/identity/authz  0.205s
# 19 个测试包通过，其余无测试；exit 0；TEST_DATABASE_URL 已设置

$ make test-integration
go test -count=1 -v ./internal/kernel/db ./internal/bootstrap ./cmd/cli -run Integration
--- PASS: TestMigrateIntegrationReversibleAndRepeatable (0.62s)
--- PASS: TestMigrateIntegrationConcurrentStartup (0.28s)
ok  github.com/omwk/go-admin-api/internal/kernel/db  2.052s
ok  github.com/omwk/go-admin-api/internal/bootstrap  55.351s
ok  github.com/omwk/go-admin-api/cmd/cli             23.306s
# exit 0，0 skipped；包含 M1a/M1b1 所有既有集成测试

$ go test -race ./internal/modules/identity/authz ./internal/kernel/cache ./internal/bootstrap -run 'TestAuthz|TestSessions|TestScope|TestAdminRepo|TestAdminMissing|TestInvalidation|TestSlow|TestVersion' -count=1
ok  github.com/omwk/go-admin-api/internal/modules/identity/authz  7.863s
ok  github.com/omwk/go-admin-api/internal/kernel/cache            2.065s
ok  github.com/omwk/go-admin-api/internal/bootstrap               124.926s
```

追加的 Redis 缓存故障/禁用账号、跨租户 sid 与 OpenAPI header 校验亦已定向复跑通过。

- [x] `make lint`：最后一行 `golangci-lint run`，exit 0。
- [x] `make test`：19 个测试包通过，exit 0。
- [x] 迁移 up/down：`go test -count=1 -v ./internal/kernel/db -run TestMigrateIntegrationReversibleAndRepeatable`（包含于上述 make 集成命令）；隔离 schema 中 up→3、down→2/1/0、up→3。无新迁移，不回滚开发库。
- [x] 新增 `/sessions` 与 `/sessions/{sid}` 已登记 §4 与 OpenAPI，实际路由/权限/operation 一一对应。
- [x] `rg -n 'TODO|not implemented' internal`：无匹配；没有保留变异 helper。
- [x] 日志敏感信息检查：`rg -n 'slog\.|logger\.|fmt\.Print|log\.Print|audit\.Event|ResourceID:' internal/modules/identity/authz internal/modules/identity/admin_sessions.go internal/bootstrap/identity_{scope,scope_write,authz,sessions}.go -g '!**/*test.go'`。失效 Warn 只含 trace/module/action/tenant；强踢审计为 sid、用户 ID 与 revoked，不含 token/hash/密码/联系方式；无调试输出。
- [x] 56 个相关 Go 文件，文件 ≤400 行、函数 ≤80 行，0 违规；PowerShell 检查方式沿用 §10 并扩展文件集合至 scope/authz/session、相关 repo 与 kernel 门面。临时 gocyclo 配置 min-complexity=12，本期新增/修改函数 0 违规；既有 M1a/M1b1 的 Login/Refresh/JWT Verify、审计 validate、BuildMenuTree、ValidateMenuMove 不在本批修改范围，保留原实现。
- [x] `git diff --name-only -- migrations go.mod go.sum`：无输出，无依赖/迁移/Go 版本变更。

偏差说明：无任务范围或契约偏差。失败期间 ≤5 分钟 TTL 收敛、鉴权依赖故障 fail closed、M3 才引入 outbox 均按既定裁决执行。遗留：等待 M1b2 人工验收；未启动 M2。
