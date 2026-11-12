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
| 2 | 菜单投影 | `GET /auth/menus` 的 `system` 下出现「部门」，其 children 含三个按钮 |
| 3 | 权限码一致 | 菜单按钮的 `permission_code` 与注册表常量逐字一致 |
| 4 | 超管增量 | 已存在的 `super_admin` 角色重跑 seed 后获得三个新码，且原有授权数量不减少 |
| 5 | 不改他人授权 | 运营/销售角色的授权集合在重跑 seed 前后完全一致 |
| 6 | 一致测试 | 新增的投影一致性测试在缺少任一投影时失败 |
| 7 | 幂等 | 连续跑两次 seed，权限与菜单数量不变、无重复 |
| 8 | 全量 | `scripts/verify.ps1 -Integration -Migration` 通过 |

## 5. 升级说明（写进文档）

已有环境升级到本版本后，需执行一次 `go run ./cmd/cli seed`：

- 管理员**密码不会被重置**（`SeedAdmin` 对已存在账号直接返回）
- 新增的权限码与菜单会被补入
- 其他角色的授权不变

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
