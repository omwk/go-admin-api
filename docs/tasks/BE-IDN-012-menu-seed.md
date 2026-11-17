# BE-IDN-012 · 菜单种子补全与归类

- ID: BE-IDN-012
- 类型: 修复
- 仓库: 后端
- 状态: 待实现
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
