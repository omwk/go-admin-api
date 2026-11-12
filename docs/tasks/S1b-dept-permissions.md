# S1b · 补齐部门权限的种子与投影

- 类型：系统层补全（S1 的遗漏）
- 预计：**0.5 人日**
- 分支：`feat/s1b-dept-permissions`
- 依赖：S1（部门 CRUD，已合并）

---

## 1. 问题

`/admin/permissions` 与 `/auth/menus` 里没有 `system:dept:create/update/delete`，
前端无法按权限码显示部门页的按钮。

## 2. 三个根因（都不是「少写了一行」）

### 2.1 种子菜单里根本没有「部门」

`seedMenus()` 只有用户/菜单/角色三个页面，没有部门页，也没有任何部门按钮：

```go
// 现状：/system 下只有 users / menus / roles
```

所以 `/auth/menus` 里既没有部门菜单，也没有可挂权限码的按钮节点。

### 2.2 已有数据库不会自动获得新权限码

`SeedPermissions` 是 upsert（会补），但**本次根本没跑过 seed**；
而且 `EnsureSeedRole` 只在角色**第一次创建**时写入授权：

```go
if created.RowsAffected > 0 {
    return s.seedRoleGrants(...)   // 角色已存在 → 直接 return
}
```

因此即使重跑 seed，`super_admin` 角色也不会补上新码。

`admin` 用户虽然 `IsSuper=true` 能绕过接口校验，但 `/auth/menus` 的权限码
来自角色授权，前端拿不到这三个码。

### 2.3 没有任何测试约束「注册的权限码必须被投影」

这是根因的主因：S1 新增了三个常量与三条路由，注册表测试通过，
但**没有任何断言检查它们是否出现在菜单/权限投影里**。同类问题会再次发生。

## 3. 改动

| # | 文件 | 改动 |
|---|---|---|
| 1 | `internal/modules/identity/seed_catalog.go` | `seedMenus()` 增加部门页与三个按钮 |
| 2 | 同上 | `seedRoles()` 的超管授权来自 `permission.All()`，无需改 |
| 3 | `internal/bootstrap/identity_seed_roles.go` | 对**内置超管角色**改为增量补授权（仅该角色） |
| 4 | 新增测试 | 权限投影一致性 |

### 3.1 菜单条目

```go
seedMenu("部门", "menu", "/system/depts", "/system", "system/dept/index", permission.SystemUserList, 4),
seedMenu("新建部门", "button", "/system/depts/create", "/system/depts", "", permission.SystemDeptCreate, 1),
seedMenu("编辑部门", "button", "/system/depts/update", "/system/depts", "", permission.SystemDeptUpdate, 2),
seedMenu("删除部门", "button", "/system/depts/delete", "/system/depts", "", permission.SystemDeptDelete, 3),
```

**为什么部门页用 `SystemUserList`**：部门读接口（S1/`GET /depts`）本身就复用了该权限码
（见 `admin_routes.go:23`）。菜单与接口用同一个码，避免「能看菜单但打不开页面」。
不为部门单独新增 `system:dept:list`，以免改动现有角色的授权。

### 3.2 超管授权改为增量

`ensureSeedRole` 对 `SuperAdminRoleCode`：即使角色已存在，也要把**缺失**的权限/菜单补上
（仅插入差集，不删除任何现有授权，不动其他角色）。

理由：内置超管的语义是「拥有全部权限」，新增权限应当自动获得；
其他角色的授权一律不动（满足「不改账号现有授权」）。

### 3.3 投影一致性测试（关键）

新增 `TestPermissionRegistryIsProjectedToMenus`：

```
permission.All() 的每个 code
  → 必须出现在 seedMenus() 的权限码集合中
  → 或在显式豁免表 menuProjectionExempt 中（附理由）
反向：seedMenus() 的每个权限码
  → 必须存在于 permission.All()
```

豁免表初始应尽量为空；确实无法挂菜单的（例如纯后端能力）逐条列明理由。

## 4. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 权限码补齐 | `GET /admin/permissions` 含三个 dept 写权限，总数 103 → 106 |
| 2 | 导航投影 | `GET /auth/menus` 的 `system` 下出现「部门」（type=menu），**children 中不含按钮** |
| 2b | 按钮权限码 | `GET /auth/menus` 的 `permissions[]` 含 `system:dept:create/update/delete` 三项 |
| 2c | 管理树 | `GET /admin/menus` 的部门节点下含三个 button 子节点，`permission_code` 正确 |
| 3 | 权限码一致 | 三处（注册表常量、`/auth/menus.permissions`、`/admin/menus` 节点）逐字一致 |
| 4 | 超管增量 | 已存在的 `super_admin` 角色重跑 seed 后获得三个新码，且原有授权数量不减少 |
| 5 | 不改他人授权 | 运营/销售角色的授权集合在重跑 seed 前后完全一致 |
| 6 | 一致测试 | 新增的投影一致性测试在缺少任一投影时失败 |
| 7 | 幂等 | 连续跑两次 seed，权限与菜单数量不变、无重复 |
| 8 | 全量 | `scripts/verify.ps1 -Integration -Migration` 通过 |

> **契约说明（2026-11-12 修订）**
> `/auth/menus` 是**导航树**，按钮节点一律过滤（`menus.go` 现有行为与测试已约束）。
> 按钮的权限码通过同响应的 `permissions[]` 平铺下发，前端据此控制按钮显隐。
> 全量树（含 button）只出现在管理端 `/admin/menus`。
> 本卡原第 2 条写成「children 含三个按钮」，与既有契约矛盾，已按上述口径修正。

## 5. 升级说明（写进文档）

已有环境升级到本版本后，需执行一次 `go run ./cmd/cli seed`：

- 管理员**密码不会被重置**（`SeedAdmin` 对已存在账号直接返回）
- 新增的权限码与菜单会被补入
- 已存在的内置 `super_admin` 角色只补齐缺失的权限与菜单关联，保留全部既有授权
- 其他角色的授权不变
- 不修改任何用户的角色关联；已撤销的用户授权不会被恢复

## 6. 不做

- 为新权限单独造 `system:dept:list`
- 修改任何用户或非超管角色的授权
- 前端页面（由前端单独提交）

## 7. Codex prompt

```
实现 docs/tasks/S1b-dept-permissions.md。

必读：本卡、internal/modules/identity/seed_catalog.go、
internal/bootstrap/identity_seed_roles.go、internal/modules/identity/permission/registry.go、
internal/modules/identity/admin_routes.go

改动：
1. seedMenus() 增加「部门」页面（/system/depts，码 system:user:list）与三个按钮
   （create/update/delete），父路径 /system 与 /system/depts
2. ensureSeedRole：内置超管角色改为增量补授权（只加差集，不删不改其他角色）
3. 新增投影一致性测试：注册表每个码必须被 seedMenus 投影，或列入显式豁免

约束：
- 不为部门新增 system:dept:list
- 不动运营/销售等角色的授权
- 不改任何用户级授权

验收按卡片 §4 八条逐条贴证据；并贴出重跑 seed 前后
sys_permission / sys_role_permission 的行数对比。
完成后停下报告，不要做前端。
```

---

## 7. 前端如何取按钮权限

/auth/menus 响应示例（节选）：

``json
{
  "menus": [
    { "name": "系统", "path": "/system", "children": [
        { "name": "用户", "path": "/system/users" },
        { "name": "菜单", "path": "/system/menus" },
        { "name": "角色", "path": "/system/roles" },
        { "name": "部门", "path": "/system/depts" }
    ]}
  ],
  "permissions": [
    "system:user:list", "system:dept:create", "system:dept:update", "system:dept:delete"
  ]
}
``

- **导航**：只看 `menus`，按钮节点不会出现，也不应该出现
- **按钮显隐**：用 `permissions` 里的码做判断，例如 `hasPerm('system:dept:create')`
- **菜单管理页**：改用管理端 `GET /admin/menus`，那里才有 button 行

## 8. 验收记录（2026-11-12）

验证使用真实 PostgreSQL 的隔离测试库，构造缺少部门权限与菜单的旧环境后，
连续执行两次真实 `SeedIdentity`。夹具另保留一项管理员自建菜单授权，验证增量补权不会删除种子之外的既有授权。

| §4 项 | 验收证据 |
|---|---|
| 1 | `TestSeedIntegrationDepartmentUpgrade` 经 HTTP 验证 `/admin/permissions` 从 103 项增至 106 项，包含三个部门写权限，且无重复码 |
| 2 | `/auth/menus` 的 `/system` 子节点中存在「部门」，`type=menu`、组件 `system/dept/index`、权限 `system:user:list`、排序 4；导航树不含 button |
| 2b | `/auth/menus.permissions` 包含 `system:dept:create`、`system:dept:update`、`system:dept:delete` |
| 2c | `/admin/menus` 的部门节点包含三个 button 子节点，父 ID、权限码及排序 1/2/3 均正确 |
| 3 | 三个按钮码逐项与注册表常量、权限目录及导航响应的平铺权限数组比对，全部一致；未引入 `system:dept:list` |
| 4 | 已存在的超管权限从 103 项补到 106 项，菜单关联只增加四项；旧权限与菜单关联均为新集合的子集，自建菜单授权保留 |
| 5 | 运营、销售的角色元数据、权限 ID 集合、菜单 ID 集合逐项相等；用户角色关联及管理员密码保持不变；非超管角色与用户已撤销授权不恢复 |
| 6 | `TestPermissionRegistryIsProjectedToMenus` 通过；分别临时移除 create/update/delete 按钮后，对应子测试均以缺失投影失败，恢复原文件后再次通过 |
| 7 | 第二次 seed 后八张身份相关表的行数完全一致；权限目录额外检查无重复码 |
| 8 | `powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1 -Integration -Migration` 退出码 0，最终输出 `all checks passed` |

### Seed 行数对比

| 表 | 重跑前 | 第一次重跑后 | 第二次重跑后 |
|---|---:|---:|---:|
| `sys_permission` | 103 | 106 | 106 |
| `sys_role_permission` | 106 | 109 | 109 |
| `sys_menu` | 11 | 15 | 15 |
| `sys_role_menu` | 17 | 21 | 21 |

菜单行数包含夹具的一个自建菜单及其超管授权；第二次重跑没有新增行。

### 验收命令与输出

```text
$env:CGO_ENABLED = '0'
$ go test ./internal/modules/identity ./internal/bootstrap -run '^(TestPermissionRegistryIsProjectedToMenus|TestSeedIntegration.*|TestIdentitySeedIntegration.*)$' -count=1 -v
registered=106 projected=9 exempt=97
seed #1: sys_permission 103 -> 106; sys_role_permission 106 -> 109; sys_menu 11 -> 15; sys_role_menu 17 -> 21
seed #2: sys_permission 106 -> 106; sys_role_permission 109 -> 109; sys_menu 15 -> 15; sys_role_menu 21 -> 21
ok github.com/omwk/go-admin-api/internal/modules/identity 1.254s
ok github.com/omwk/go-admin-api/internal/bootstrap 3.776s

$ powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-test-groups.ps1
TestSeedIntegrationDepartmentUpgrade -> core
Source 428 = shards 427 + excluded 1
PASS: every integration test has exactly one shard or an explicit exclusion.

$ powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1 -Integration -Migration
== go vet ==
== golangci-lint ==
== go test ==
ok github.com/omwk/go-admin-api/internal/bootstrap 299.401s
== integration ==
ok github.com/omwk/go-admin-api/internal/kernel/db 13.534s
ok github.com/omwk/go-admin-api/internal/bootstrap 336.519s
ok github.com/omwk/go-admin-api/cmd/cli 11.356s
== migrate up/down/up ==
version=24 dirty=false
version=23 dirty=false
version=24 dirty=false
all checks passed
```

一致性测试对当前最小菜单种子的 97 项既有缺口逐码列出豁免理由；本次部门写权限全部有真实投影，
不在豁免中。测试同时拒绝未知码投影、未知码豁免、无理由豁免及已有投影的过期豁免。

### 自检与偏差

- 本机未安装 `make`；使用 `verify.ps1` 中等价的 `golangci-lint run`、`go test ./...`，均通过。
- 迁移实际执行 `go run ./cmd/cli migrate up`、`down`、`up`，版本与结果见上。
- `rg -n 'TODO|not implemented' internal/` 无匹配；改动未增加生产日志，新增测试日志仅输出数量、权限码和验证状态。
- `gofmt -l` 检查五个改动 Go 文件，无输出；使用临时标准库 Go AST 检查器执行以下命令，最大文件 200 行、最大函数 40 行，均满足 400/80 限制。

  ```powershell
  go run "$env:TEMP/codex-s1b-size-check.go" -- internal/modules/identity/seed_catalog.go internal/bootstrap/identity_seed_roles.go internal/bootstrap/identity_seed_revocation_integration_test.go internal/modules/identity/seed_projection_test.go internal/bootstrap/identity_seed_dept_integration_test.go
  ```

- 未新增或变更接口契约；实现范围与修订后的任务卡一致。
