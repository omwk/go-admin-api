# BE-IDN-012 · 菜单种子补全与归类

- ID: BE-IDN-012
- 类型: 修复
- 仓库: 后端
- 状态: 已实现并本地验收（开发库 Redis 缓存失效待环境恢复）
- 预计: 1 人日
- 分支: `feat/be-idn-012-menu-seed`
- 依赖: 无

---

## 1. 现象

用户在前端看不到「商品管理」「数据字典」「系统配置」，但页面确实能打开。

核查结果：**后端菜单种子只有 21 条，缺了 4 个可见页面**；
前端为了能用，自己在 `router/` 里合成路由补上。

### 1.1 缺口清单（后端无菜单、前端有页面）

| 页面 | 路径 | 应有权限 | 前端现状 |
|---|---|---|---|
| 商品管理 | `/catalog/products` | `product:spu:list` | `catalog.ts` 硬编码 |
| 商品详情 | `/catalog/products/:id` | `product:spu:read` | 同上（hideInMenu） |
| 数据字典 | `/system/dicts` | `system:dict:list` | `configuration-menus.ts` 硬编码 |
| 系统配置 | `/system/settings` | `system:setting:list` | 同上 |
| 租户模板 | `/system/tenants/templates` | `system:tenant:manage` | `tenant-menus.ts` 硬编码 |
| 模板编辑 | `/system/tenants/templates/:id` | `system:tenant:manage` | 同上 |

前端代码里甚至留了注释承认这件事：

```ts
/** The live menu seed has no catalog entry; use the verified API permission. */
```

### 1.2 归类问题

| 现状 | 问题 |
|---|---|
| 商品管理没有父目录 | 侧边栏平铺，和「系统/交易/客户」不同级 |
| 数据字典、系统配置不在「系统」下 | 只能靠前端硬编码路由访问 |
| 租户模板不在「租户管理」下 | 子页面游离 |

## 2. 改动

### 2.1 补齐菜单种子

```
系统 (dir, /system, sort 1)
├ 用户 /system/users                        sort 1  system:user:list
├ 菜单 /system/menus                        sort 2  system:menu:list
├ 角色 /system/roles                        sort 3  system:role:list
├ 部门 /system/depts                        sort 4  system:user:list
├ 通知中心 /system/notifications             sort 5  system:notify:list
├ 通知渠道 /system/notifications/channels    sort 6  system:notify:manage
├ 审计日志 /system/audit-logs                sort 7  system:audit:list
├ 登录历史 /system/login-logs                sort 8  system:audit:list
├ 租户管理 /system/tenants                   sort 9  system:tenant:list
│  ├ 租户模板 /system/tenants/templates      sort 1  system:tenant:manage  visible=false
│  └ 模板编辑 /system/tenants/templates/:id  sort 2  system:tenant:manage  visible=false
├ 数据字典 /system/dicts                     sort 10 system:dict:list          ← 新增
└ 系统配置 /system/settings                  sort 11 system:setting:list       ← 新增

商品 (dir, /catalog, sort 3)                                                  ← 新增
└ 商品管理 /catalog/products                sort 1  product:spu:list          ← 新增
   └ 商品详情 /catalog/products/:id         sort 1  product:spu:read  visible=false ← 新增

交易 (dir, /trade, sort 2)
└ 订单 /trade/orders                        sort 1  trade:order:list

客户 (dir, /crm, sort 4)                                                      ← 排序后移
├ 客户列表 /crm/customers                    sort 1  crm:customer:list
└ 询盘 /crm/inquiries                        sort 2  crm:inquiry:list
```

组件路径（与前端组件注册表一致）：

```
system/dict/index   /system/dict/index.vue
system/setting/index /system/setting/index.vue
catalog/product/index  /catalog/product/index.vue
catalog/product/detail /catalog/product/detail.vue
system/tenant/index    /system/tenant/index.vue
system/tenant/template-editor /system/tenant/template-editor.vue
```

### 2.2 种子对已存在菜单的更新语义

现有 `seed` 只插入不更新，所以已在库里的租户不会得到新菜单。
本卡要求改为**按 (tenant_id, path) upsert**：

| 字段 | 行为 |
|---|---|
| name / type / component / redirect / icon / sort / visible / keep_alive / permission_code | 以代码为准覆盖 |
| 自定义菜单（代码中不存在） | **保留，不删除** |

并保证重复执行幂等。

### 2.3 菜单与页面的覆盖守卫

新增测试 `TestMenuSeedCoversDeclaredPages`：

- 维护一份「后端承诺的页面清单」（路径 + 权限码 + 组件）
- 断言每种租户种子生成后，清单里每一项都能在菜单树中找到
- 新增页面但忘记加菜单 → 测试失败

## 3. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 新租户 | 新建租户后 `/auth/menus` 含商品、数据字典、系统配置、租户模板（可见性正确） |
| 2 | 已存在租户 | 重跑 seed 后默认租户菜单从 21 条增至 33 条（按实际） |
| 3 | 归类 | 数据字典/系统配置在「系统」下；商品管理在「商品」下；模板在「租户管理」下 |
| 4 | 排序 | 目录顺序：系统 1、交易 2、商品 3、客户 4 |
| 5 | 隐藏页 | 商品详情与模板编辑的菜单 visible=false，不出现在侧边栏 |
| 6 | 幂等 | 连续两次 seed，菜单条数与内容不变 |
| 7 | 自定义保留 | 手工新增的菜单在重跑 seed 后仍存在 |
| 8 | 权限 | 无 `product:spu:list` 的用户看不到商品菜单 |
| 9 | 覆盖守卫 | 页面清单中临时删掉一条菜单 → 测试失败 |
| 10 | 契约 | `/auth/menus` 响应结构不变；OpenAPI 无变化 |
| 11 | 全量 | `verify` 通过 |

## 4. 不做

- 删除前端 fallback 路由（另卡）
- 交易/客户页面的实际实现（仍是 pending 占位）
- 菜单图标体系调整

## 5. Codex prompt

```
实现 docs/tasks/BE-IDN-012-menu-seed.md。

分支：feat/be-idn-012-menu-seed（从最新 main c4e323a 开）

必读：本卡、internal/modules/identity/seed_catalog.go、seed.go、
internal/modules/identity/permission/registry.go（product:spu:* 与 system:dict/setting）、
internal/modules/identity/model/menu.go（visible/keep_alive 字段）、
家 migrations/000002_identity_init.up.sql 的 sys_menu 定义

改动：
1. 补 6 条菜单 + 1 个「商品」目录；调整客户目录 sort
2. seed 改为按 (tenant_id, path) upsert：更新代码字段，保留自定义菜单
3. 新增页面清单与覆盖守卫测试
4. 重跑 seed 验证现有租户菜单补齐

约束：
- 不删除任何已有菜单
- 不改 /auth/menus 响应结构
- 图标沿用现有风格（无图标则留空）

验收按卡片 §3 十一条逐条贴证据，第 2 条贴重跑前后菜单条数与树结构。
完成后停下报告，不要做前端。
```

## 6. 实施计划（2026-11-17）

- [x] 从 `30df5be` 创建 `feat/be-idn-012-menu-seed`，读取约定、种子、权限与菜单存储定义。
- [x] 先添加 `seed_pages_test.go` 的独立页面清单和覆盖守卫；修复前已验证缺少模板页时失败。
- [x] 添加 `identity_seed_menu_integration_test.go`，验证旧租户升级、ID 保留、完整字段更新、幂等、自定义与跨租户隔离、模板新租户和导航权限。
- [x] `seed_catalog.go` 追加六页与商品目录，保持原数组位置；移除 `seed_projection_test.go` 中已覆盖权限的豁免。
- [x] `identity_seed.go` 在既有事务锁下按 `(tenant_id, path)` 查询，更新显式字段与父级，保留自定义记录。
- [x] 运行定向回归、页面缺失负向实验、分片守卫、`scripts/verify.ps1` 与隔离库迁移全链测试。
- [x] 记录开发库默认租户 seed 前后完整菜单树，连续运行两次 seed 比较内容；清理本卡验收噪声，逐条填写 §3 证据。

## 7. 验收记录（2026-11-17）

基线 `30df5be`，分支 `feat/be-idn-012-menu-seed`。必读路径 `model/menu.go` 在当前仓库中实际为 `model/model.go` 的 `Menu`；权限常量 `system:dict:*` / `system:setting:*` 位于 `permission/system.go`。

### 7.1 十一项结果

| # | 证据 | 结果 |
|---|---|---|
| 1 | `TestSeedMenuIntegrationNewTenantNavigationAndPermissions` 通过：真实模板快照、创建租户、接受邀请、登录、请求 `/api/v1/auth/menus`；新租户 28 条菜单，商品/字典/配置可见，三个隐藏页面存在于库中但不进入侧边栏。 | 通过 |
| 2 | 开发库 `tenant_id=1` 连续两次 `go run ./cmd/cli seed`，实际 `21 → 28 → 28`；原 21 个 ID 全部保留，新增 7、删除 0；完整前后树见 §7.2。 | 数据库通过；缓存告警见 §7.4 |
| 3 | `assertSeedMenuGraph` 对六页逐项比对真实父 ID：字典/配置→系统，商品管理→商品，商品详情→商品管理，模板页→租户管理。 | 通过 |
| 4 | 新租户 HTTP 响应根节点精确断言 `[/system /trade /catalog /crm]`；库中 sort 分别为 `1/2/3/4`。 | 通过 |
| 5 | 商品详情、租户模板、模板编辑均 `visible=false`；HTTP 树中商品管理和租户管理的隐藏子页均被过滤。 | 通过 |
| 6 | 两次开发库 seed 完整菜单 JSON 的 SHA256 均为 `0a2c94b1f6aff487f01f0af9b4d7d4674364732cea1389748fea93f6f754fd87`；升级集成测试同时断言两次结果逐字段相等。 | 通过 |
| 7 | `TestSeedMenuIntegrationPreservesCustomAndForeignMenus` 通过：手工创建自定义菜单，重跑后逐字段相等；另一租户同种子路径的菜单也不变。 | 通过 |
| 8 | 新租户集成测试同时使用运营角色：权限中无 `product:spu:list`，`/auth/menus` 不含商品管理。 | 通过 |
| 9 | 正常 `TestMenuSeedCoversDeclaredPages`：租户 1/23 均 `declared pages=18 menus=28`；Go overlay 临时删除字典菜单后两者均失败，精确报 `missing declared page /system/dicts`，实际源码保持正常。 | 通过 |
| 10 | 新租户 `/auth/menus` 响应用既有 OpenAPI 校验通过；`git diff 30df5be -- api/openapi internal/modules/identity/menus.go internal/modules/identity/profile.go` 无输出。无新增接口。 | 通过 |
| 11 | `powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1`，退出码 0，最后一行 `all checks passed`；45 个测试包通过。 | 通过 |

### 7.2 开发库重跑前后菜单树

以下包含按钮与隐藏页；`hidden` 表示 `visible=false`。排序数字均为数据库实值。

重跑前：21 条。

```text
系统 /system sort=1
├ 用户 /system/users sort=1
├ 菜单 /system/menus sort=2
├ 角色 /system/roles sort=3
├ 部门 /system/depts sort=4
│ ├ 新建部门 /system/depts/create sort=1 [button]
│ ├ 编辑部门 /system/depts/update sort=2 [button]
│ └ 删除部门 /system/depts/delete sort=3 [button]
├ 通知中心 /system/notifications sort=5
│ └ 标记已读 /system/notifications/read sort=1 [button]
├ 通知渠道 /system/notifications/channels sort=6
├ 审计日志 /system/audit-logs sort=7
├ 登录历史 /system/login-logs sort=8
└ 租户管理 /system/tenants sort=9
  └ 管理租户 /system/tenants/manage sort=1 [button]
交易 /trade sort=2
└ 订单 /trade/orders sort=1
  └ 查看订单 /trade/orders/view sort=1 [button]
客户 /crm sort=3
├ 客户列表 /crm/customers sort=1
└ 询盘 /crm/inquiries sort=2
```

重跑后：28 条；第二次与第一次完全一致。

```text
系统 /system sort=1
├ 用户 /system/users sort=1
├ 菜单 /system/menus sort=2
├ 角色 /system/roles sort=3
├ 部门 /system/depts sort=4
│ ├ 新建部门 /system/depts/create sort=1 [button]
│ ├ 编辑部门 /system/depts/update sort=2 [button]
│ └ 删除部门 /system/depts/delete sort=3 [button]
├ 通知中心 /system/notifications sort=5
│ └ 标记已读 /system/notifications/read sort=1 [button]
├ 通知渠道 /system/notifications/channels sort=6
├ 审计日志 /system/audit-logs sort=7
├ 登录历史 /system/login-logs sort=8
├ 租户管理 /system/tenants sort=9
│ ├ 管理租户 /system/tenants/manage sort=1 [button]
│ ├ 租户模板 /system/tenants/templates sort=1 [hidden]
│ └ 模板编辑 /system/tenants/templates/:id sort=2 [hidden]
├ 数据字典 /system/dicts sort=10
└ 系统配置 /system/settings sort=11
交易 /trade sort=2
└ 订单 /trade/orders sort=1
  └ 查看订单 /trade/orders/view sort=1 [button]
商品 /catalog sort=3
└ 商品管理 /catalog/products sort=1
  └ 商品详情 /catalog/products/:id sort=1 [hidden]
客户 /crm sort=4
├ 客户列表 /crm/customers sort=1
└ 询盘 /crm/inquiries sort=2
```

### 7.3 命令与检查

本机设置 `CGO_ENABLED=0`，构建缓存使用系统 `AppData/Local/go-build`；未新增依赖或迁移。

```text
$ go test ./internal/modules/identity ./internal/bootstrap -run 'TestMenuSeed|TestPermissionRegistryIsProjectedToMenus|TestSeedMenuIntegration|TestSeedRepository' -count=1 -v
tenant=1 declared pages=18 menus=28
tenant=23 declared pages=18 menus=28
menus 21 -> 28 -> 28; stable IDs, updated type/parent/fields, identical second seed
new tenant=2: 28 menus; root order 1/2/3/4; hidden routes stored, omitted from sidebar; OpenAPI valid
ok github.com/omwk/go-admin-api/internal/bootstrap 5.383s

$ powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-test-groups.ps1
Source 579 = shards 578 + excluded 1
PASS: every integration test has exactly one shard or an explicit exclusion.

$ go test ./internal/kernel/db -run '^TestMigrateIntegrationReversibleAndRepeatable$' -count=1 -v
--- PASS: TestMigrateIntegrationReversibleAndRepeatable (5.68s)
ok github.com/omwk/go-admin-api/internal/kernel/db 5.819s

$ powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1
== go vet ==
== golangci-lint ==
== go test ==
ok github.com/omwk/go-admin-api/cmd/cli 71.428s
ok github.com/omwk/go-admin-api/internal/bootstrap 425.986s
ok github.com/omwk/go-admin-api/internal/kernel/db 15.335s
ok github.com/omwk/go-admin-api/internal/modules/identity 52.752s
all checks passed
```

- `make` 未安装；verify 中直接执行了相同的 `golangci-lint run` 和 `go test ./...`，并额外执行 `go vet ./...`。
- 迁移测试在隔离库实际执行全链 up、逐步 down 到版本 0、再 up，未回滚开发库。
- `rg -n 'TODO|not implemented' internal` 无匹配；`gofmt -l` 对改动 Go 文件无输出；`git diff --check` 通过。
- 对改动文件执行 PowerShell `Get-Content` 行计数与 `^func` 至顶层 `}` 的行跨度检查：最大单文件 145 行、最大函数 34 行。
- 日志检查：新增生产代码没有日志输出；验收工具仅输出菜单字段、条数和 SHA256，未输出连接串或凭据。
- 已清理：集成测试结束后隔离数据库自动销毁；开发库审计计数 `14 → 14 → 14`，本卡新增审计 0、临时租户/用户/角色/模板 0、临时配置修改 0，无本卡数据需要调用 `audit-prune` 删除；保留既有审计，保留本卡正式新增的 7 条菜单。

### 7.4 偏差与环境限制

- 实际菜单数为 `21 + 7 = 28`；卡片 §3.2 的 `33` 标注了“按实际”，这里记录真实结果。
- 数据库无 `(tenant_id, path)` 唯一约束；在现有事务及 seed/management 租户级 advisory lock 内执行查询和显式更新/插入，未新增 schema 变更。
- 开发库两次 CLI 均输出 `warning: seed committed but authorization cache invalidation failed`；默认 `localhost:6379` 无可用 Redis，环境未提供替代地址。数据库菜单已提交，现有会话缓存失效仍待 Redis 恢复后重跑 seed。真实 CLI 缓存失效集成测试随全量测试通过，但不替代开发环境结果。
- 本地未运行 race；本机无 C 编译器，项目规定由 CI 执行。
