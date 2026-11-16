# BE-IDN-010 · 租户模板可编辑

- ID: BE-IDN-010
- 类型: 新增
- 仓库: 后端
- 状态: 已完成（2026-11-16，后端本地验收通过）
- 预计: 1.5 人日
- 分支: `feat/be-idn-010-template-editable`
- 依赖: S5（租户模板与开通，已合并）

---

## 1. 问题

现状：

```
POST /admin/tenant-templates  { source_tenant_id: <必填>, code, name }
PUT  /admin/tenant-templates/{id}  { name, description }     ← 只能改名
```

由此产生三个后果：

1. **必须有一个"样板租户"才能建模板** —— 而样板租户只有 default，
   所以实际上永远只有一个模板
2. **建出来的模板改不了** —— 想做个「只读版」（2 个角色、无写权限），
   只能先真的开一个这样的租户，再克隆它
3. **模板无法复用** —— 两个模板想只差一个角色的权限，得克隆两次再分别建租户

模板应该是**可编辑的角色与权限组合**，不是租户的只读快照。

## 2. 目标

```
模板 = 一组角色定义（角色名 + 数据范围 + 权限码 + 可见菜单）
     + 菜单树（框架菜单，通常所有模板一致）

创建方式二选一：从租户快照 / 空白模板
创建之后：可以增删改角色、调整每个角色的权限
```

## 3. 改动

### 3.1 创建支持空白模板

```
POST /admin/tenant-templates
{ "code": "readonly", "name": "只读版", "description": "...", "source_tenant_id": null }
```

- `source_tenant_id` 省略 → 空白模板：`roles=[]`，`menus` 取当前租户的框架菜单树
  （菜单是框架对象，与租户无关，空白模板也必须有，否则开通后侧边栏为空）
- 提供 `source_tenant_id` → 行为与现在一致

### 3.2 更新支持编辑角色

```
PUT /admin/tenant-templates/{id}
{
  "name": "...", "description": "...",
  "roles": [
    { "code": "operator", "name": "运营", "data_scope": "self", "status": "active",
      "permissions": ["trade:order:list", "trade:order:read"],
      "menus": ["/trade", "/trade/orders"] },
    { "code": "viewer", "name": "只读", "data_scope": "self", "status": "active",
      "permissions": ["trade:order:list"], "menus": ["/trade", "/trade/orders"] }
  ]
}
```

校验：

| 项 | 规则 |
|---|---|
| role.code | 必填、唯一、≤64，`^[a-z][a-z0-9_]*$` |
| role.name | 必填、≤128 |
| role.data_scope | `all / dept / dept_and_child / self / custom` |
| role.permissions | 每个码必须在权限注册表中存在；去重 |
| role.menus | 每个路径必须存在于模板的 menus 中；去重 |
| roles 数量 | 0–50；为 0 时允许保存，但开通时提示「该模板不含角色」 |

`menus` 本身**不允许通过 PUT 修改**（框架菜单树的增删属于平台升级，不属于租户模板）。

### 3.3 复制模板

```
POST /admin/tenant-templates/{id}/clone  { "code": "readonly2", "name": "只读版-2" }
```

用途：在已有模板上做小改动（只差一个角色的权限）时，不必从头建。

### 3.4 模板详情返回可编辑结构

```
GET /admin/tenant-templates/{id}
→ { id, code, name, description, roles:[...], menus:[...], created_at, updated_at }
```

`roles` 直接返回可回填的结构；`menus` 返回路径与名称（用于前端展示可勾选项）。

## 4. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 空白模板 | 不传 source_tenant_id 创建成功，roles 为空、menus 非空 |
| 2 | 编辑角色 | PUT 提交 2 个角色，GET 回读一致 |
| 3 | 权限校验 | 不存在的权限码 → 400 且指明是哪个码 |
| 4 | 菜单校验 | 角色引用模板中不存在的菜单路径 → 400 |
| 5 | 角色唯一 | 重复 role.code → 400 |
| 6 | 空角色 | roles=[] 可保存；开通时返回警告字段（非错误） |
| 7 | 复制 | clone 后两模板角色独立，改一个不影响另一个 |
| 8 | 开通验证 | 用「只读版」模板开通租户：角色数与权限与模板一致 |
| 9 | 旧模板兼容 | 从租户快照创建的模板可正常编辑与开通 |
| 10 | 越权 | 非平台管理员操作模板 → 404 |
| 11 | 契约 | OpenAPI 更新，双向一致 |
| 12 | 全量 | `verify` 通过 |

## 5. 不做

- 模板版本管理（只保留最新）
- 模板继承（模板 A 基于模板 B）
- 通过 PUT 修改菜单树
- 前端页面（另卡）

## 6. Codex prompt

```
实现 docs/tasks/BE-IDN-010-template-editable.md。

分支：feat/be-idn-010-template-editable（从最新 main 开）

必读：本卡、internal/modules/identity/tenant_template.go、
internal/bootstrap/identity_tenant_snapshot.go、identity_tenant_materialize.go、
internal/modules/identity/model/tenant.go（TemplateRole/TemplateMenu）、
internal/modules/identity/permission/registry.go、api/openapi/system.yaml

改动：
1. POST 模板：source_tenant_id 可选；省略时建空白模板（menus 取当前租户框架菜单）
2. PUT 模板：支持 roles 全量替换（校验 code/name/data_scope/permissions/menus）
3. POST /tenant-templates/{id}/clone
4. GET 详情返回可编辑的 roles 与 menus
5. 开通时若模板 roles 为空，返回 warning 字段而非错误

约束：
- menus 不允许通过 PUT 修改
- 权限码必须存在于注册表；菜单路径必须存在于模板 menus
- 不新增表；模板 JSON 结构不变

验收按卡片 §4 十二条逐条贴证据，第 8 条要用「只读版」模板真实开通一个租户验证。
完成后停下报告，不要做前端。
```

## 7. 实施计划（2026-11-16）

基线：`7ac896b`；分支：`feat/be-idn-010-template-editable`。沿用现有管理事务、幂等与 JSON 快照，不新增依赖或迁移。

- [x] 在 `internal/bootstrap/identity_tenant_template_*_integration_test.go` 补充空白创建、角色替换/去重、参数拒绝、复制独立、空角色 warning、只读模板真实开通与旧快照兼容回归；先运行空白创建用例确认旧实现失败（201 != 400）。
- [x] 修改 `internal/modules/identity/tenant_template.go`，将角色校验与权限目录补齐拆到同目录 `tenant_template_roles.go`；`repo/tenant.go` 与 `internal/bootstrap/identity_tenant_store.go` 原子保存角色及所需权限目录，菜单原样保留。
- [x] 修改 `tenant_handlers.go`、`tenant_http.go` 注册复制接口并让所有非平台身份访问模板均返回 404；`tenant_provision.go` 在空角色成功开通时附加 warning；调整 `model/tenant.go` 注释。
- [x] 修改 `api/openapi/system.yaml` 的创建/更新/复制/开通契约；保留元信息更新兼容性（省略 roles 保留原角色，显式 [] 清空）。更新既有边界用例以符合本卡 404 要求。
- [x] 执行定向集成、双向 OpenAPI、分片守卫、`scripts/verify.ps1`，以及隔离数据库的迁移全链 up/down/up。当前 `.env` 使普通 `go test ./...` 已执行真实数据库集成测试，未重复追加 `-Integration`。逐条证据及清理结果见 §9–11。

验收入口：`go test -count=1 -v ./internal/bootstrap -run '^TestIdentityTenantTemplateIntegration'`；真实 PostgreSQL 中创建的租户逐项查询角色、权限与菜单授权，不使用替身。测试数据库由现有隔离 fixture 创建并在结束时销毁。

## 8. 接口表

统一前缀 `/api/v1/admin`；模板接口仅配置的平台租户超级管理员可访问，其余已认证身份返回 404。

| 方法 | 路径 | 输入 / 行为 | 成功响应 |
|---|---|---|---|
| POST | `/tenant-templates` | `source_tenant_id` 可省略或 null；此时角色为空、菜单取当前租户；传正整数保留原快照行为 | 201，模板完整对象 |
| PUT | `/tenant-templates/{id}` | `name` 必填；`description` 可选；传 `roles` 全量替换，0–50 个；省略或 null 保留旧角色；禁止顶层 `menus`、`permissions`、`settings`、`code` | 200，模板完整对象 |
| POST | `/tenant-templates/{id}/clone` | `code`、`name` 必填；完整复制角色、菜单、权限目录、描述和设置；复用现有幂等事务 | 201，独立模板对象；重复 code 409 |
| GET | `/tenant-templates/{id}` | 返回可回填角色及菜单 key/path/name；旧的无路径、重复路径菜单仍使用稳定 key | 200，模板完整对象 |
| POST | `/tenants` | 沿用原开通请求；模板角色为空仍正常创建框架对象和待激活管理员 | 201；空角色附加 `warning: "该模板不含角色"`，非空省略 warning |

角色字段：code/name/data_scope 必填；status 缺省 active，接受 active/disabled；permissions、menus 缺省归一为空数组。权限从代码注册表校验；旧快照缺少的已注册权限目录项按需补齐，不修改菜单树。

## 9. 验收记录（2026-11-16）

以下缩写用例统一带 `TestIdentityTenantTemplateIntegration` 前缀。请求经真实 Gin 路由、管理事务、仓储及 PostgreSQL 执行；响应同时按 OpenAPI 校验。

| # | 验收项 | 可复核证据 |
|---|---|---|
| 1 | 空白模板 | `BlankAndEdit`：省略 source 返回 201，`roles=[]`、menus 非空；`OptionalSourceAndLimits`：null 同样成功，0/-1 返回 400、不存在的 source 返回 404。 |
| 2 | 编辑角色 | `BlankAndEdit`：PUT 2 个角色后 GET 与输入逐字段相等；权限和菜单重复项去重，模板菜单 JSON 前后相同。 |
| 3 | 权限校验 | `RoleValidation/unknown_permission`：400 / 10001，响应包含 `trade:order:unknown`；失败后名称和角色均未被修改。 |
| 4 | 菜单校验 | `RoleValidation/unknown_menu`：引用 `/not-in-template` 返回 400；`OptionalSourceAndLimits`：PUT 顶层 menus 的对象、数组、字符串均返回 400。 |
| 5 | 角色唯一 | `RoleValidation`：重复 code 返回 400；空/非法/65 字符 code、空/129 字符名称、非法 scope/status、51 个角色同样拒绝；64 字符 code、128 字符中文名、50 个角色及全部五种 scope 均接受。 |
| 6 | 空角色 | `EmptyWarning`：显式 `roles=[]` 保存成功；开通返回 201 和 `warning="该模板不含角色"`；库中角色数为 0、菜单和待激活管理员存在；幂等重放返回相同结果。 |
| 7 | 复制 | `CloneIndependent`：复制结果 ID/code 独立，角色/菜单/权限目录/描述/设置一致；修改副本不影响原模板，反向修改也隔离；同键重放 201、重复 code 409。 |
| 8 | 真实开通只读版 | `ReadonlyProvision`：真实 PostgreSQL 隔离库中开通 `readonly-customer`，tenant_id=2；精确 2 个角色、3 条角色权限授权、4 条角色菜单授权；operator 只有 `trade:order:list` / `trade:order:read`，viewer 只有 `trade:order:list`；均为 active/self，无写权限授权。管理员绑定的活跃角色数也与模板一致。 |
| 9 | 旧模板兼容 | `SnapshotEditable`：从 source=1 快照创建后编辑并真实开通，角色/授权精确一致；缺少的已注册权限目录项自动补齐；`LegacyMenuKeys`：无路径/重复路径菜单的原稳定 key 可回填且授予准确菜单。既有 `TestIdentityTenantIntegrationTemplateMetadataAndProtection` 通过。 |
| 10 | 越权 | `Boundaries`：普通平台 operator、普通租户超级管理员对 list/create/get/put/delete/clone 全部返回 404 / 10003；存在和不存在的目标表现一致。原租户权限不足 403 的断言仍保留。 |
| 11 | 契约 | `TestSystemOpenAPIRoutesAndPermissionsMatch`：`actual=47 manifest=47 OpenAPI=47; tenant operations=14; permissions match in both directions`；新增接口已列入 §8。 |
| 12 | 全量 | `powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1` 返回 0，最后一行 `all checks passed`；包含 go vet、golangci-lint、go test ./...。 |

定向验收命令及汇总：

```text
go test -count=1 -v ./internal/bootstrap -run '^(TestIdentityTenantTemplateIntegration|TestIdentityTenantIntegrationTemplateMetadataAndProtection|TestIdentityTenantIntegrationPlatformAndOrdinaryBoundaries|TestSystemOpenAPIRoutesAndPermissionsMatch)'
PASS
ok  github.com/omwk/go-admin-api/internal/bootstrap  8.061s
```

迁移在隔离 schema/数据库中验证，不对开发业务 schema 执行回滚：

```text
go test -count=1 -v ./internal/kernel/db -run '^(TestMigrateIntegrationReversibleAndRepeatable|TestMigrateIntegrationConcurrentStartup|TestMigrationIntegration.*)$'
--- PASS: TestMigrateIntegrationReversibleAndRepeatable (7.18s)
--- PASS: TestMigrateIntegrationConcurrentStartup (1.95s)
--- PASS: TestMigrationIntegrationPartitionInvariants (1.01s)
--- PASS: TestMigrationIntegrationTenantAndIdempotencyUniqueness (0.19s)
--- PASS: TestMigrationIntegrationVersionDefaultsAndCompositeKeys (0.15s)
PASS
ok  github.com/omwk/go-admin-api/internal/kernel/db  10.605s
```

其中可逆测试实际执行全链 up、重复 up、逐次 down 到 0、断言无残留业务表，再全链 up 至最新版本。

分片守卫：

```text
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-test-groups.ps1
Source 565 = shards 564 + excluded 1
PASS: every integration test has exactly one shard or an explicit exclusion.
```

新增 10 个集成测试均归属 core 分片。审计一致性测试发现新增 clone action 需要登记，已补齐 `docs/design/audit-events.md`；重跑完整 verify 后审计包通过（0.496s），未放宽守卫。

## 10. 自检

- [x] `make lint` 等价命令 `golangci-lint run` 通过：由 verify 调用，成功无输出、退出 0，最终 `all checks passed`。本机没有 make，使用既有 PowerShell 入口。
- [x] `make test` 等价命令 `go test ./...` 通过：verify 汇总 `all checks passed`；此前全量 bootstrap 实跑 436.908s、cmd/cli 94.952s、kernel/db 23.281s；审计登记修复后完整 verify 重跑通过，其余包复用成功缓存。
- [x] 迁移 up/down/up 与并发启动、结构不变量通过：命令和输出见 §9；没有新增或修改迁移。
- [x] 新增 clone 接口及修改契约已记录在 §8，双向一致性通过。
- [x] `rg -n 'TODO|not implemented' internal/` 无匹配（退出 1）；`git diff --check` 通过。
- [x] 日志检查：人工检查变更 diff 与新增文件，没有新增日志打印；审计仅包含框架角色定义、名称和资源 ID，不记录凭据或邀请链接。创建租户响应中的邀请链接沿用原有加密幂等存储。
- [x] Go AST 检查变更及新增的 15 个 Go 文件：`Reviewed 15 changed Go files: max file=188 lines; max function=57 lines`，`PASS: file <=400 and function <=80`。命令：`go run "$env:TEMP/be-idn-010-review.go" -- @templateChangedGo`；文件集合来自 git diff 与 git ls-files 未跟踪 Go 文件。

## 11. 清理记录

已清理：现有 fixture 在 t.Cleanup 中执行隔离库 `DROP DATABASE IF EXISTS <fixture_database>`、隔离迁移 schema `DROP SCHEMA <fixture_schema> CASCADE`，业务数据及审计一并删除；清理失败会使测试失败。另用只读事务查询本次验收/迁移日志中的全部 15 个数据库名称：

```text
SELECT count(*) FROM pg_database WHERE datname = ANY($1::text[])
Checked 15 isolated acceptance/migration databases; remaining=0
PASS: isolated acceptance data and audit rows removed with database cleanup
```

本卡没有向开发库业务 schema 写入测试租户、用户、角色或审计，也未修改开发系统配置；无需对开发库执行 audit-prune，未保留本卡验收业务数据。上述只读清理核验没有修改数据库。

## 12. 偏差与边界

- 功能偏差：无。没有新增依赖、表或前端改动，模板 JSON 结构保持兼容。
- 原模板权限不足用例按本卡要求调整为 404，其他租户接口 403 断言和双向契约守卫保持严格。
- 环境差异：以 verify 内的等价命令代替不可用的 make；本机无 gcc，race 留待 CI，未将本地通过解释为 race 已通过。
