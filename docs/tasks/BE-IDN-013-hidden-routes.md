# BE-IDN-013 · 隐藏路由投影

- ID: BE-IDN-013
- 类型: 修复
- 仓库: 后端
- 状态: 待实现
- 预计: 0.3 人日
- 分支: `feat/be-idn-013-hidden-routes`
- 依赖: BE-IDN-012（菜单种子）

---

## 1. 问题

BE-IDN-012 补齐了菜单，其中「商品详情」「租户模板」「模板编辑」是 `visible=false`
的隐藏页。

但 `/auth/menus` 当时**只返回 visible 菜单**，于是前端拿不到这三条路由：

- 删掉前端合成路由后（FE-IDN-009），直接访问 `/catalog/products/124` 会 404
- 刷新页面同样 404

隐藏页的「隐藏」应该只影响**侧边栏展示**，不影响**路由注册**。

## 2. 改动

### 2.1 投影包含隐藏页

`profileStore.Menus` 去掉 `visible = true` 过滤，`BuildMenuTree` 同样不再丢弃隐藏行，
并在 `MenuNode` 增加 `visible` 字段：

```json
{ "id": 40, "path": "/catalog/products/:id", "visible": false, "component": "..." }
```

- 侧边栏渲染：`visible=false` 不显示
- 路由注册：照常注册

### 2.2 缓存键版本化

旧的缓存 payload 不含 `visible` 字段，直接复用会让前端拿到过期结构。
菜单缓存键追加 `:routes-v1`，升级即失效，不需要手工清 Redis。

### 2.3 契约

OpenAPI 的 `Menu` 增加必填 `visible`；`/auth/menus` 描述改为
「授权的非按钮路由，含隐藏页；visible 只控制侧边栏」。

## 3. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 隐藏页在响应中 | `/auth/menus` 含 `/catalog/products/:id`、`/system/tenants/templates`，`visible=false` |
| 2 | 可见页不变 | 可见菜单仍 `visible=true`，数量与之前一致 |
| 3 | 权限仍生效 | 无对应权限的用户拿不到隐藏页路由 |
| 4 | 按钮不出现 | `type=button` 仍被过滤 |
| 5 | 缓存隔离 | 升级后不会读到旧结构（新键无命中）；旧键不影响 |
| 6 | 契约 | OpenAPI 双向校验通过（`visible` 必填） |
| 7 | 全量 | `verify` 通过 |

## 4. 不做

- 前端渲染逻辑（FE-IDN-009）
- 菜单排序与归类（BE-IDN-012）

## 5. Codex prompt

```
实现 docs/tasks/BE-IDN-013-hidden-routes.md。

分支：feat/be-idn-013-hidden-routes（从最新 main 开）

必读：本卡、internal/modules/identity/menus.go、menus_test.go、
internal/bootstrap/identity_profile.go、authz/cache.go、
api/openapi/identity.yaml

改动：
1. Menus 查询去掉 visible=true 过滤；BuildMenuTree 不再丢弃隐藏行
2. MenuNode 增加 visible 字段（必填）
3. 菜单缓存键追加 :routes-v1
4. OpenAPI 同步

约束：
- 仍过滤 status<>active 与 type=button
- 权限过滤逻辑不变

验收按卡片 §3 七条逐条贴证据。完成后停下报告，不要做前端。
```
