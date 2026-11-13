# S5 · 租户管理与开通（模板快照）

- 类型：系统层补全（`docs/design/system-layer-plan.md` S5）
- 预计：**4 人日**
- 分支：`feat/s5-tenant`
- 依赖：S1–S4（默认菜单/角色/权限已齐）

---

## 1. 决策：默认角色与菜单从「模板快照」来

不硬编码默认角色，也不每次从某个租户实时复制，而是**快照模板**：

```
从现有租户「保存为模板」→ tenant_template（含角色与菜单的快照）
开通新租户 → 从模板物化 → 建首个管理员邀请
```

理由：

| 方案 | 优 | 劣 |
|---|---|---|
| 硬编码默认角色 | 简单 | 改一次要发版；不同业务线无法用不同初始权限 |
| 从指定租户**实时**复制 | 灵活 | 源租户被改动后，下次开通结果悄悄变了，不可复现 |
| **模板快照（选定）** | 可复现、可审阅、可多套模板 | 多一张表与一次物化逻辑 |

模板只存**框架层**对象：角色、角色-权限、角色-菜单、系统配置（可选）。
**不复制**业务数据（店铺/商品/订单）。

## 2. 数据模型

```
tenant_template (
  id, code, name, description,
  roles jsonb,        -- [{code,name,data_scope,permissions:[codes],menus:[paths]}]
  settings jsonb,     -- 可选：租户级配置覆盖
  created_at, updated_at
  唯一 (code)
)
```

`tenant` 表补一列：

```
provisioned_from bigint null   -- 来源模板 id，便于追溯
```

## 3. 接口

### 3.1 模板

```
GET    /admin/tenant-templates
POST   /admin/tenant-templates                 从现有租户生成快照 { source_tenant_id, code, name }
GET    /admin/tenant-templates/{id}
PUT    /admin/tenant-templates/{id}            改名称/描述（不改快照内容）
DELETE /admin/tenant-templates/{id}            未被使用才可删
```

权限：`system:tenant:manage`。

### 3.2 租户

```
GET    /admin/tenants                          列表（含状态、创建时间、来源模板）
GET    /admin/tenants/{id}
POST   /admin/tenants                          开通：{ code, name, template_id, admin_username, admin_email? }
PUT    /admin/tenants/{id}                     改名（code 不可改）
POST   /admin/tenants/{id}/disable             停用
POST   /admin/tenants/{id}/enable              启用
```

权限：`system:tenant:list` / `system:tenant:manage`。

### 3.3 开通行为（关键）

`POST /admin/tenants` 在**同一事务**内：

1. 校验 `code` 唯一（全局，不区分租户）
2. 建 `tenant` 行
3. 按模板物化角色与授权（角色 code 冲突 → 换新租户内不冲突）
4. 按模板物化菜单（按钮节点一并创建）
5. 创建管理员用户（`is_super=true`，随机初始密码 **不落库不明文**，改用**邀请令牌**）
6. 返回 `{ tenant_id, invite_url }` —— 管理员点链接自己设密码

> 用邀请而非初始密码：与 F1 一致，避免「初始密码经手多人」。

### 3.4 平台管理员边界（2026-11-13 补充）

跨租户管理权限只授予**平台管理员**：

``
平台管理员 = 平台租户（tenant.PlatformID，默认 1）内 is_super=true 的用户
其他租户的管理员跨租户访问 → 404（不是 403，避免暴露目标是否存在）
``

约束：

1. **平台租户 ID 取配置** `tenant.platform_id`，默认 1，禁止硬编码
2. **禁止停用平台租户**：`POST /admin/tenants/{platformID}/disable` → 409
   「平台租户不可停用」；否则停用后无人能再启用，形成不可恢复死锁
3. **跨租户操作必须审计**，同时记录 actor_tenant 与 target_tenant
4. 平台管理员对自己租户的操作与普通管理员一致，不特殊放大

### 3.5 停用语义

- `disable` → 该租户所有用户登录返回 403/20111「租户已停用」
- 已登录会话：下次刷新令牌时拒绝
- 数据保留，不删除

## 4. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 生成模板 | 从租户 A 生成模板，快照含 A 的全部角色与菜单路径 |
| 2 | 开通租户 | 用模板开通 B：角色数量、权限、菜单与模板一致 |
| 3 | 邀请 | 返回 invite_url，点开可设密码并登录 B |
| 4 | 隔离 | B 的数据对 A 不可见；B 管理员看不到 A 的用户 |
| 5 | 菜单 | B 登录后侧边栏与 A 一致（按钮权限码齐全） |
| 6 | code 唯一 | 重复 code 开通 → 409 |
| 7 | 事务 | 物化中途失败 → 租户行不残留（模拟权限缺失） |
| 8 | 停用 | 停用后 B 的用户登录 403；刷新令牌失败 |
| 9 | 启用 | 重新启用后可登录 |
| 10 | 模板保护 | 被使用的模板删除 → 409 |
| 11 | 越权 | A 的管理员读写 B 的租户 → 404 |
| 12 | 权限 | 无 `system:tenant:manage` → 403 |
| 13 | 菜单投影 | 「租户管理」页出现在系统目录；投影一致性测试通过 |
| 14 | 契约 | 新增接口进 OpenAPI，双向一致 |
| 15 | 全量 | `scripts/verify.ps1 -Integration -Migration` 通过 |

## 5. 不做

- 租户计费/套餐绑定（属业务层）
- 租户数据导出与删除
- 跨租户用户（已定：一用户一租户）
- 模板版本管理（只保留最新快照）

## 6. Codex prompt

```
实现 docs/tasks/S5-tenant.md。

必读：本卡、docs/design/system-layer-plan.md S5、
internal/modules/identity/（seed 与权限物化逻辑可复用）、
internal/bootstrap/（事务与审计写法）、
api/openapi/identity.yaml、system.yaml

范围：
1. 0000xx 迁移：tenant_template 表 + tenant.provisioned_from
2. 模板 CRUD（从现有租户生成快照）
3. 租户开通（同一事务物化角色/菜单 + 建管理员 + 返回邀请链接）
4. 停用/启用（登录与刷新令牌都要拦）
5. 权限码 system:tenant:list / manage 与菜单投影

约束：
- 物化只复制角色/权限/菜单/可选配置，不复制业务数据
- 管理员用邀请令牌，不生成明文初始密码
- code 全局唯一；停用不删数据
- 迁移号在 S4 之后（若 S4 用了 000028，则本卡用 000029）

验收按卡片 §4 十五条逐条贴证据。
完成后停下报告，不要做前端。
```

## 7. 平台边界验收（补充）

| # | 项 | 断言 |
|---|---|---|
| 16 | 平台管理员跨租户 | 平台租户超管可停用/启用其他租户 |
| 17 | 普通管理员跨租户 | 非平台租户管理员访问他人租户 → 404 |
| 18 | 停用平台租户 | 返回 409，状态不变 |
| 19 | 审计字段 | 跨租户操作的审计同时含 actor_tenant 与 target_tenant |
| 20 | 配置化 | `tenant.platform_id` 改成 2 后，边界随之转移（用测试验证，不改默认值） |

## 8. 实现记录

- 迁移固定为 `000029_tenant_template`。在 §2 的示意字段上补 `menus` 与 `permissions` JSONB，保存完整菜单图与权限元数据，避免物化时重新读取源租户。源 ID 不作为新租户 ID 使用；菜单用快照内 key / parent_key 关联，物化时生成新 ID。
- 模板快照内容及 code 不可修改；只允许名称/描述更新。全局模板及开通操作仅由平台管理员执行；普通管理员保留自己租户目录的读取、改名和启用/停用权限，跨租户先返回 404。
- `tenant.platform_id` 通过配置文件或 `CH_TENANT_PLATFORM_ID` 设置，默认 1，非正数配置拒绝启动。平台租户禁止停用。
- 开通先创建禁用且无可用密码的超管，邀请绑定该账号、7 天过期、单次使用；接受邀请原子激活原账号。回执 `invite_url` 使用 F1 链接格式，响应 `Cache-Control: no-store`；可选幂等回执加密保存，审计不含邀请、密码或邮箱。
- 本批不复制业务数据，也不启用可选配置复制；`settings` 为空对象。不复制源用户、部门树或自定义部门授权，根部门在新租户重新创建。
- 11 个新增操作见 §3，完整请求、响应、权限与错误契约见 `api/openapi/system.yaml`；登录选租户和停用错误见 `api/openapi/identity.yaml`。前端不在本次范围。

## 9. 验收证据（2026-11-14）

以下用例均在真实 PostgreSQL 的隔离测试数据库执行。表中的 `TenantIntegration` 前缀展开为 `TestIdentityTenantIntegration`，源码位于 `internal/bootstrap/identity_tenant_*_integration_test.go`。

```powershell
go test -count=1 -v ./internal/bootstrap ./internal/modules/identity/... -run '^(TestSystemMetadataIntegrationErrorCodesAndPermissions|TestIdentityBootstrapIntegrationRegistersUsableRoutes|TestIdentityAccountOwnershipIntegrationTenantAndSessionIsolation|TestSystemDictScopeIntegrationMigration|TestAuditPermissionAndMenusAppendAfterS3a|TestTenantPermissionsAndMenusAppendAfterS4|TestIdentityTenantIntegration.*)$'
# PASS; internal/bootstrap 13.592s; internal/modules/identity 0.614s
```

| # | 验收项 | 执行证据与断言 |
|---|---|---|
| 1 | 生成模板 | `TenantIntegrationSnapshotProvisionAndInvite`：全部角色、权限、菜单父子关系与授权一致；含隐藏、未分配、按钮、无路径及重复路径节点；修改源租户后快照不变。 |
| 2 | 开通租户 | 同一用例对物化前后规范化快照作完整 JSON 相等断言，并验证 `provisioned_from`。 |
| 3 | 管理员邀请 | 同一用例：预建超管为 disabled、密码哈希为空；接受邀请返回 201，登录 B 返回 200；再次接受返回 400，仍只有一个用户。`TenantIntegrationInvitationExpiresAndIsBound` 另验过期及用户名/租户绑定。 |
| 4 | 隔离 | 同一用例：A 列表无 B 管理员；B 列表无 A 用户、读取 A 用户为 404；B 店铺、商品、订单数量为 0。 |
| 5 | 菜单与按钮 | 同一用例比较 A/B `/auth/menus` 的规范化菜单与全部权限码，包含 `system:tenant:manage`。 |
| 6 | code 唯一 | `TenantIntegrationCodeAndEncryptedIdempotency`：重复全局 code 返回 409；同幂等键原样回放，改请求体返回 409/10006；持久化回执不含明文邀请。 |
| 7 | 原子事务 | `TenantIntegrationMaterializationRollsBack`：注入缺失权限，在租户和菜单已创建后失败；租户、角色、菜单、授权、部门、管理员、邀请、审计、幂等表计数全部不变。`TenantIntegrationAuditFailureRollsBack` 另验审计失败回滚。 |
| 8 | 停用 | `TenantIntegrationSuspendAndResume`：登录和刷新均为 403/20111；刷新令牌行及数量不变，用户和数据保留。 |
| 9 | 启用 | 同一用例：启用后登录与原刷新令牌均返回 200。 |
| 10 | 模板保护 | `TenantIntegrationTemplateMetadataAndProtection`：被引用模板删除为 409；未引用模板可删且随后读取 404；更新仅允许名称/描述。 |
| 11 | 租户越权 | `TenantIntegrationPlatformAndOrdinaryBoundaries`：普通管理员读写现存及不存在的外租户，均返回 404/10003。 |
| 12 | 权限 | 同一用例：撤掉超管及相应授权后，租户列表、开通、启用、模板操作均返回 403。 |
| 13 | 菜单投影 | `TestTenantPermissionsAndMenusAppendAfterS4`：系统目录下新增租户页及管理按钮；保留 S4 权限/菜单原数组位置。完整投影另由 #5 验证。 |
| 14 | OpenAPI | `TestSystemOpenAPIRoutesAndPermissionsMatch` 核对真实路由、权限清单、OpenAPI 三方；S5 新增 11 个操作。专项请求同时校验实际响应 schema。 |
| 15 | 全量验证 | `powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1 -Integration -Migration` 退出码 0，末行为 `all checks passed`；全仓测试、显式集成测试、迁移 up/down/up 均通过，版本为 29 → 28 → 29 且 dirty=false。 |
| 16 | 平台跨租户 | `TenantIntegrationSuspendAndResume`：平台管理员停用/启用 B 均返回 200。 |
| 17 | 普通管理员边界 | `TenantIntegrationPlatformAndOrdinaryBoundaries`：B 超管跨租户 GET/PUT/disable/enable 对现存和不存在目标均为 404；本租户改名为 200。 |
| 18 | 平台保护 | 同一用例：停用配置的平台租户为 409「平台租户不可停用」，数据库状态仍为 active。 |
| 19 | 跨租户审计 | `TenantIntegrationCrossTenantAudit`：开通、详情、列表、改名、停用、启用共 6 条审计，before/after 均含 `actor_tenant` 与 `target_tenant`，无邀请凭据。 |
| 20 | 配置化边界 | `TenantIntegrationPlatformConfigurationMovesBoundary` 从配置文件加载 `platform_id: 2`，通过生产路由装配验证：租户 2 超管可停启租户 1；原平台超管跨租户为 404；停用租户 2 为 409。`internal/kernel/config/tenant_test.go` 另验默认 1、文件/环境覆盖及非法值拒绝。 |

补充检查：`scripts/check-test-groups.ps1` 输出 `Source 485 = shards 484 + excluded 1`，并确认每个集成测试恰好归属一个分片；新增 S5 测试归属 core，无新增排除项。

全仓回归中同步维护了新增错误码后的目录数量、S4 菜单历史位置、启动测试的平台配置，以及迁移 28 的回退目标：仍严格验证回到版本 27 后 `scope` 列消失，再恢复最新版本。跨租户无效邀请继续返回既有的「邀请或链接无效」。没有删除或放宽行为断言。

### 全量命令与实际输出

本机未安装 `make`，验证入口执行 Makefile 的等价命令 `golangci-lint run`、`go test ./...`，同时补充 `go vet`、显式集成测试和迁移往返；未新增工具或 Go 依赖。

```text
$ powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1 -Integration -Migration
== go vet ==
== golangci-lint ==
== go test ==
ok  github.com/omwk/go-admin-api/internal/bootstrap 411.839s
ok  github.com/omwk/go-admin-api/internal/modules/identity 31.773s
ok  github.com/omwk/go-admin-api/internal/modules/identity/permission 3.150s
== integration ==
ok  github.com/omwk/go-admin-api/internal/kernel/db 10.996s
ok  github.com/omwk/go-admin-api/internal/bootstrap 514.555s
ok  github.com/omwk/go-admin-api/cmd/cli 41.432s
== migrate up/down/up ==
version=29 dirty=false
version=28 dirty=false
version=29 dirty=false
all checks passed
```

迁移阶段实际执行 `go run ./cmd/cli migrate up`、`go run ./cmd/cli migrate down`、`go run ./cmd/cli migrate up`。数据库集成包同时覆盖完整链 up、down 到底再 up、并发启动和结构不变量。

### 自检

- [x] lint / vet：上述完整命令退出码 0，lint 无告警。
- [x] 全仓测试及显式集成测试：上述输出通过。
- [x] 迁移 up/down/up：29 → 28 → 29，全部 dirty=false。
- [x] 11 个新增接口已记录在 §3、OpenAPI、API 盘点和系统层实现说明。
- [x] `rg -n 'TODO|not implemented|fmt\.Print|log\.Print' internal/` 无匹配；未留空实现或调试输出。
- [x] 检查新增日志/审计写入点：仅记录资源 ID、code、模板/管理员 ID、名称或状态与租户边界；不写密码、邀请、邮箱。跨租户审计和幂等测试另断言邀请凭据不以明文持久化。
- [x] `go run "$env:TEMP\codex-s5-tenant\size.go"` 输出 `checked 39 changed Go files`：AST 检查无文件超过 400 行、无函数超过 80 行。
- [x] 集成分片守卫通过：`Source 485 = shards 484 + excluded 1`。
- [x] 仅新增迁移 000029；未编辑已合并迁移、未新增第三方依赖、未改前端。

偏差：可选配置复制本批未启用；完整快照增加 `menus` / `permissions` JSONB 的表示说明见 §8。命令环境差异为未安装 make，已执行等价命令。Windows 本机无 gcc/CGO，race 检查按仓库约定由 CI 执行，本次未运行 CI。
