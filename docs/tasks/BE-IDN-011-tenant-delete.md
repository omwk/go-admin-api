# BE-IDN-011 · 租户归档与彻底删除

- ID: BE-IDN-011
- 类型: 新增
- 仓库: 后端
- 状态: 已实现，本地验收通过，待审阅
- 预计: 1.5 人日
- 分支: `feat/be-idn-011-tenant-delete`
- 依赖: S5（租户管理）、BE-IDN-002（管理员恢复）

---

## 1. 为什么现在要做

S5 的结论是「不做删除，停用即归档」。这条保守决定带来两个问题：

1. 测试/废弃租户只能停用，列表里越积越多
2. 真正要清理时只能手写 SQL 按外键顺序删 15 张表——**比提供接口更危险**

删租户是高危操作，但**不给工具不等于没人会做**，只会让做法更不可控。

## 2. 三段式模型

| 阶段 | 接口 | 状态 | 数据 | 可恢复 |
|---|---|---|---|---|
| 正常 | — | active | 有 | — |
| 停用 | `POST /admin/tenants/{id}/disable` | suspended | 有 | 立即 |
| **归档** | `POST /admin/tenants/{id}/archive` | suspended + `deleted_at` | 有 | 只有平台管理员可恢复 |
| **彻底删除** | `DELETE /admin/tenants/{id}` | 行消失 | 无 | 不可 |

### 2.1 归档

```
POST /admin/tenants/{id}/archive        归档（= 软删除）
POST /admin/tenants/{id}/restore        恢复
```

- 写 `deleted_at = now()`
- 登录、刷新、API 访问全部按停用处理（403/20111）
- 列表默认不返回归档租户；`?include_archived=true` 才显示
- 详情按 id 仍可查（便于恢复）
- 已有 active 用户的租户归档时直接成功（用户随之无法登录），不需要先停用

### 2.2 彻底删除

```
DELETE /admin/tenants/{id}?confirm=<tenant_code>
```

前置条件（全部满足才执行）：

| 条件 | 失败返回 |
|---|---|
| 平台管理员 | 404 |
| 非平台租户 | 409「平台租户不可删除」 |
| 已归档（`deleted_at` 非空） | 409「请先归档」 |
| 归档满 `tenant.purge_grace_days`（默认 7 天） | 409「归档未满 N 天」 |
| `confirm` 与租户 code 完全一致 | 400「确认码不匹配」 |

执行：

- **单事务**按外键依赖顺序删除该租户全部数据（见 §3）
- 删除前统计各表行数，写审计：`system.tenant.purge`，`after` 记录 `{code, rows:{table:count}}`
- 任一步失败整体回滚

### 2.3 配置

```
tenant.purge_grace_days   number  0-365  默认 7
```

0 表示允许归档后立即删除（仅建议测试环境）。

## 3. 级联删除范围

按当前 schema 实测，需要按顺序删除（外键在前）：

```
sys_role_permission → sys_role_menu → sys_user_role
password_history → sys_refresh_token → auth_token
notify_delivery → notify_outbox → notify_template → channel_provider
notification → notification_preference
sys_dict_item → sys_dict
sys_menu → sys_role → sys_user → sys_dept → sys_permission
tenant
```

要求：

- 清单集中在一个函数里，**新增带 tenant_id 的表时必须同步登记**（配扫描测试：
  所有 `tenant_id` 列的表必须在清单中，或显式豁免）
- 删除语句参数化，按 tenant_id 过滤
- 每表删除行数计入审计

## 4. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 归档 | `archive` 后列表默认不返回；`include_archived=true` 返回且带 deleted_at |
| 2 | 归档登录 | 该租户用户登录 403/20111 |
| 3 | 恢复 | `restore` 后可正常登录，列表恢复显示 |
| 4 | 删除前置 | 未归档删除 → 409；归档未满宽限期 → 409 |
| 5 | 确认码 | `confirm` 错误 → 400；正确 → 204 |
| 6 | 平台租户 | 归档/删除均 409 |
| 7 | 删除彻底 | 删除后 tenant 行不存在；其用户/角色/菜单/字典/令牌计数均为 0 |
| 8 | 事务 | 注入中途失败 → 所有表计数不变、租户仍存在 |
| 9 | 审计 | 一条 `system.tenant.purge`，含各表行数，不含敏感数据 |
| 10 | 清单守卫 | 新增带 tenant_id 的表未登记 → 测试失败 |
| 11 | 宽限期配置 | `purge_grace_days=0` 时归档后可立即删除 |
| 12 | 越权 | 非平台管理员调用 → 404 |
| 13 | 契约 | OpenAPI 更新，双向一致 |
| 14 | 全量 | `verify` 通过 |

## 5. 不做

- 租户数据导出（删除前备份）
- 异步后台清理（本卡为同步单事务）
- 批量删除多个租户
- 前端页面（另卡）

## 实施计划与已确认边界（2026-11-16）

用户已确认方案 B：仅彻底删除没有业务数据的租户。业务表逐表显式豁免并检查，存在数据返回 409；审计记录保持只追加。补齐原清单遗漏的 `sys_role_dept`、内核配置和运行数据，所有删除均带租户条件。

- [x] 迁移与配置：新增 `000032_tenant_archive` 的 up/down；模型增加 `DeletedAt *time.Time`；注册 `tenant.purge_grace_days`，整数范围 0–365、默认 7。宽限期从配置的平台租户读取，防止被删除租户降低平台保护策略。
- [x] 生命周期：`ArchiveTenant` / `RestoreTenant` 仅平台管理员可调用；在现有 `AdminService.Execute` 事务中锁定租户行，归档设置 suspended 和 UTC 时间；恢复设置 active 并清空时间。重复归档保留首次时间；enable 不允许绕过归档。
- [x] 删除：`PurgeTenant(ctx, actor, id, confirm) error` 检查平台权限、平台租户、归档、宽限期、完全匹配的确认码；调用集中清单，统计与删除、`system.tenant.purge` 审计在同一事务。`after={code,rows}` 不放用户/凭据内容。
- [x] HTTP 和列表：增加 archive/restore/DELETE 路由；新路由在参数校验前隐藏非平台身份为 404；DELETE 的幂等摘要包含 confirm；列表 `include_archived` 默认 false，详情仍可查。
- [x] 回归：生命周期、登录/刷新/API 拦截、0/7/365 天边界、确认码、越权、平台保护、完整计数、注入失败回滚、审计失败回滚、其他租户/全局通知配置不变；扫描实际 schema 的 tenant_id 表并验证未登记表会失败。
- [x] 契约与交付：更新现有 system OpenAPI，验证双向路由；执行 verify、全链迁移与分片守卫；按 §4 提供十四项证据。所有集成验收使用测试隔离库，清理由 fixture 自动执行。

实现文件集中于 `internal/modules/identity/tenant*.go`、其 model/repo、`internal/bootstrap/identity_tenant*.go`、`internal/kernel/setting/`、新迁移、现有 system OpenAPI 和本任务卡。遵循既有 Go/GORM/PostgreSQL/registry 接口，不新增依赖。

## 接口契约（本卡新增）

下表路径均带 `/api/v1` 前缀；写接口支持可选 `Idempotency-Key`。

| 方法 | 路径 | 成功 | 权限与参数 |
|---|---|---|---|
| POST | `/admin/tenants/{id}/archive` | 200，tenant 对象 | 仅平台管理员；平台租户 409 |
| POST | `/admin/tenants/{id}/restore` | 200，tenant 对象 | 仅平台管理员；未归档 409 |
| DELETE | `/admin/tenants/{id}?confirm=<tenant_code>` | 204，无响应体 | 仅平台管理员；归档、宽限期、确认码检查；有业务数据 409 |
| GET | `/admin/tenants?include_archived=true` | 200，分页列表 | 原列表权限；默认 false；非法布尔值 400 |

三个新增写接口对非平台管理员均返回 404；`deleted_at` 为 RFC3339 UTC 或 null。

## 验收记录（2026-11-17）

分支 `feat/be-idn-011-tenant-delete`，基于 `fb59c92`。只实现后端，未提交或推送。

| # | 实测证据 |
|---|---|
| 1 | `TestIdentityTenantArchiveIntegrationLifecycle`：archive=200；默认列表隐藏，include_archived=true 显示且带 UTC deleted_at；详情仍为 200。 |
| 2 | 同一用例：归档后的 login、refresh、既有 access token 的 API 请求均为 403/20111。 |
| 3 | 同一用例：restore=200、deleted_at=null；列表重新显示，登录=200，已有数据计数保持。重复归档保留首次时间，enable 不得绕过归档。 |
| 4 | `TestIdentityTenantPurgeIntegrationPreconditionsAndReplay`：未归档 409；默认宽限 7 天时 409；目标租户自行设为 0 不生效；恰好满 7 天可删除。 |
| 5 | 同一用例：缺失、大小写不同、尾随空格、错误及 SQL 注入式确认码均为 400；完全匹配为 204；相同幂等键重放 204，改变确认码为 409/10006。 |
| 6 | `TestIdentityTenantArchiveIntegrationPlatformOnly` 及删除前置用例：平台租户归档/删除均为 409。 |
| 7 | `TestIdentityTenantPurgeIntegrationCompleteAndAudited`：24 张表删除后计数见下表；包含已软删除用户。平台、另一个填充相同夹具的租户、全局 NULL tenant_id 通知配置的逐表内容指纹不变。 |
| 8 | `TestIdentityTenantPurgeIntegrationFailureRollsBack`：在 sys_user 的 DELETE 触发器注入失败，返回 500；下表前后计数一致，全部内容指纹一致，tenant 仍 suspended；无 purge 审计或幂等结果。另一个用例验证全部 DELETE 之后审计写入失败也整体回滚。 |
| 9 | 成功操作恰好一条 `system.tenant.purge`，归属平台操作者租户；after 只有 code 和 24 项非负整数行数，实际内容见下文。令牌表只记录计数。 |
| 10 | `TestIdentityTenantPurgeIntegrationManifestGuard`：扫描实际 schema 的 95 张 tenant_id 主表；创建未登记表后报 `tenant table missing purge registration: tenant_purge_unregistered`，移除后通过；分区子表由父表覆盖。 |
| 11 | `TestIdentityTenantPurgeIntegrationGraceSettings`：默认 7；0/365/7 写入成功；-1/366/小数/字符串/null/bool 均为 400。成功删除用例以 0 天即时归档删除。 |
| 12 | `TestIdentityTenantPurgeIntegrationPermissions`：普通租户超级管理员对自己、平台、不存在目标的 archive/restore/purge 全部 404；平台普通 operator 删除亦为 404。 |
| 13 | `TestSystemOpenAPIRoutesAndPermissionsMatch` 输出：`actual=50 manifest=50 OpenAPI=50; tenant operations=17; permissions match in both directions`。system 与 identity 的配置契约均同步。 |
| 14 | `powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1 -Integration`：go vet、golangci-lint、go test ./... 及全量 DB/bootstrap/CLI 集成测试通过，最后一行 `all checks passed`。 |

第 7、8 条逐表计数，均为实际数据库查询结果。失败在删除完关联、令牌、通知、字典等数据后触发：

| 表 | 成功删除后（#7） | 注入失败前（#8） | 回滚后（#8） |
|---|---:|---:|---:|
| auth_token | 0 | 1 | 1 |
| channel_provider | 0 | 1 | 1 |
| idempotency_key | 0 | 1 | 1 |
| notification | 0 | 1 | 1 |
| notification_preference | 0 | 1 | 1 |
| notify_delivery | 0 | 1 | 1 |
| notify_outbox | 0 | 1 | 1 |
| notify_template | 0 | 1 | 1 |
| outbox_event | 0 | 1 | 1 |
| password_history | 0 | 1 | 1 |
| sys_dept | 0 | 1 | 1 |
| sys_dict | 0 | 9 | 9 |
| sys_dict_item | 0 | 33 | 33 |
| sys_menu | 0 | 21 | 21 |
| sys_permission | 0 | 118 | 118 |
| sys_refresh_token | 0 | 1 | 1 |
| sys_role | 0 | 3 | 3 |
| sys_role_dept | 0 | 1 | 1 |
| sys_role_menu | 0 | 27 | 27 |
| sys_role_permission | 0 | 121 | 121 |
| sys_setting | 0 | 1 | 1 |
| sys_user | 0 | 2 | 2 |
| sys_user_role | 0 | 3 | 3 |
| tenant | 0 | 1 | 1 |

第 9 条实际审计内容（`tenant_id=1`、`action=system.tenant.purge`、`resource_id=2`、`result=success`）：

```json
{"code":"purge-b","rows":{"tenant":1,"sys_dept":1,"sys_dict":9,"sys_menu":21,"sys_role":3,"sys_user":2,"auth_token":1,"sys_setting":1,"notification":1,"outbox_event":1,"notify_outbox":1,"sys_dict_item":33,"sys_role_dept":1,"sys_role_menu":27,"sys_user_role":3,"sys_permission":118,"idempotency_key":1,"notify_delivery":1,"notify_template":1,"channel_provider":1,"password_history":1,"sys_refresh_token":1,"sys_role_permission":121,"notification_preference":1}}
```

复现命令与末尾输出（专项均 `-count=1`，没有跳过）：

```powershell
$env:CH_DB_TXN_TIMEOUT='15s'
$env:CH_DB_MIGRATE_TIMEOUT='5m'
$env:CH_CACHE_OP_TIMEOUT='5s'
$env:CH_SYNC_REPLAY_BATCH_TIMEOUT='5s'
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1 -Integration
# go test ./...: internal/bootstrap 391.004s；cmd/cli 43.589s；internal/kernel/db 14.609s
# integration: internal/kernel/db 8.000s；internal/bootstrap 375.959s；cmd/cli 32.810s
# all checks passed

go test -count=1 -v ./internal/bootstrap -run 'Test(IdentityTenant(Archive|Purge)Integration|SystemOpenAPIRoutesAndPermissionsMatch|AuthenticateIntegrationUsesOneExistenceQuery)'
# 最终专项同次执行：ok github.com/omwk/go-admin-api/internal/bootstrap 12.233s

go test -count=1 -v ./internal/kernel/db -run 'Test(MigrateIntegration(ReversibleAndRepeatable|ConcurrentStartup)|MigrationIntegration(PartitionInvariants|TenantAndIdempotencyUniqueness|VersionDefaultsAndCompositeKeys)|MigrateAuditSemanticsIntegration)'
# ok github.com/omwk/go-admin-api/internal/kernel/db 9.445s
# ReversibleAndRepeatable 内部执行：up → up → version → down 到 0 → up 到 32

powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-test-groups.ps1
# Source 576 = shards 575 + excluded 1
# PASS: every integration test has exactly one shard or an explicit exclusion.
```

自检与边界：

- [x] 本机未安装 make；verify 已执行 Makefile 中同样的 `golangci-lint run`、`go test ./...`；lint 无诊断、exit 0，脚本最终 `all checks passed`。
- [x] 迁移在隔离库全链 up/down/up、并发启动及结构不变量通过；仅新增 000032，未编辑已合并迁移。
- [x] 新增接口及 include_archived 已记录，OpenAPI 双向一致。
- [x] `rg -n 'TODO|not implemented' internal/`：0 匹配；检查新增日志、审计及失败 HTTP 响应，不包含用户/凭据内容，内部失败原因不会回传。
- [x] 28 个变更 Go 文件：`go run "$env:TEMP/be-idn-009-sizecheck.go" -- <变更 Go 文件列表>` 输出 `all changed Go files <=400 lines; functions <=80 lines`；`gofmt -l <同一列表>` 无输出；`git diff --check` 无诊断。
- [x] Go 缓存使用仓库外默认目录；未新增第三方依赖。并发 race 检查仍由 CI 执行，本机无 CGO/gcc。
- [x] 已清理：fixture 的 `DROP DATABASE IF EXISTS <本次隔离库>` / `DROP SCHEMA <本次隔离 schema> CASCADE` → 测试清理无错误；本次未向开发库 public 写入验收数据、审计或临时配置，无保留测试业务数据。

偏差说明：按用户明确确认的 B 方案，业务表逐表显式豁免并检查，任意业务数据阻止删除（409）；审计记录保留。补齐原系统清单漏列的 sys_role_dept、sys_setting、outbox_event、idempotency_key。既有历史迁移测试改为回滚到指定的 v30 再验证完整升级，所有行为断言保留；这是 AGENTS.md §3 已允许的迁移测试维护。

## 6. Codex prompt

```
实现 docs/tasks/BE-IDN-011-tenant-delete.md。

分支：feat/be-idn-011-tenant-delete（从最新 main 开）

必读：本卡、internal/modules/identity/tenant*.go、
internal/bootstrap/identity_tenant_store.go、identity_tenant_materialize.go、
migrations/ 现有 tenant 与 tenant-scoped 表、internal/kernel/setting/（配置读取）

改动：
1. 迁移 000032：tenant.deleted_at + 索引；配置 tenant.purge_grace_days（默认 7）
2. POST /admin/tenants/{id}/archive 与 /restore
3. DELETE /admin/tenants/{id}?confirm=<code>：仅平台管理员、需已归档且过宽限期
4. 级联删除清单集中实现，单事务；删除行数写审计
5. 列表支持 include_archived（默认 false）
6. 扫描测试：所有含 tenant_id 的表必须在删除清单中或显式豁免

约束：
- 平台租户不可归档/删除
- 删除不可逆；失败必须整体回滚
- 不删除其他租户的任何行

验收按卡片 §4 十四条逐条贴证据：
- 第 7 条贴删除后各表计数
- 第 8 条贴失败回滚后的计数
- 第 9 条贴审计内容
完成后停下报告，不要做前端。
```
