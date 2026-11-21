# BE-IDN-026 · 菜单组件路径统一为简写

- ID: BE-IDN-026
- 类型: 重构
- 仓库: 后端
- 状态: 待实现
- 预计: 0.2 人日
- 分支: `feat/be-idn-026-menu-component`
- 依赖: BE-IDN-024

---

## 1. 问题

菜单 `component` 字段两种写法并存：

```
用户      system/user/index          简写
数据字典  system/dict/index          简写
存储配置  /system/storage/index.vue  全路径
文件管理  /system/files/index.vue    全路径
```

前端注册表为兼容两种写法，同一个页面注册了两个键：

```ts
'/system/dict/index.vue': …
'/system/dicts/index.vue': …
```

新增页面时很容易只写一种，导致路由解析失败。

## 2. 决定

**统一使用简写**：`<domain>/<page>/index`（不带前导斜杠、不带 `.vue`）。

| 菜单 | component |
|---|---|
| 用户 | `system/user/index` |
| 存储配置 | `system/storage/index` |
| 文件管理 | `system/files/index` |

前端注册表同步改为简写键（见 FE-IDN-018），并由 resolver 统一加前缀。

## 2.1 顺带：文件列表排除头像

上传头像会在 `file_object` 里留下一条 `purpose=avatar` 记录（实测 id=12、avatar.jpg），
于是**文件管理列表里会出现所有用户的头像**，与「文件库」语义不符。

要求：

- `GET /admin/files` 默认排除 `purpose=avatar`
- 提供 `include_avatar=true` 时才返回（供审计排查）
- 配额统计仍包含头像占用（避免绕过配额）

## 3. 改动

1. `seed_catalog.go` 中所有 `component` 改为简写
2. seed upsert 时更新已存在菜单的 component（BE-IDN-012 的 upsert 已支持）
3. 覆盖守卫测试：断言所有 seed 的 component 匹配 `^[a-z0-9/-]+$`（无前导斜杠、无 `.vue`）
4. 迁移旧值：seed 重跑即生效，无需 SQL 迁移

## 4. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 全部简写 | seed 中所有 component 匹配 `^[a-z0-9/-]+$` |
| 2 | 重跑生效 | 重跑 seed 后 `/auth/menus` 全部为简写 |
| 3 | 守卫 | 故意写一条 `/x/y.vue` → 测试失败 |
| 4 | 前端可用 | 配合 FE-IDN-018 后，所有页面可打开 |
| 4.1 | 头像不出现 | 文件列表默认不含 purpose=avatar；include_avatar=true 时出现 |
| 4.2 | 配额不绕过 | 头像占用计入 usage |
| 5 | 全量 | `verify` 通过 |

## 5. Codex prompt

```
实现 docs/tasks/BE-IDN-026-menu-component.md。

分支：feat/be-idn-026-menu-component（从最新 main c22c535 开）

必读：本卡、internal/modules/identity/seed_catalog.go、
seed_pages_test.go（覆盖守卫）、identity_seed.go（upsert）

改动：
1. 所有菜单 component 改为简写 <domain>/<page>/index
2. 增加格式守卫：^[a-z0-9/-]+$，禁止前导斜杠与 .vue 后缀
3. 重跑 seed 验证已存在菜单被更新

约束：
- 不改菜单 path 与权限码
- 与 FE-IDN-018 配套上线（前端不兼容会导致白屏，需同时合并）

验收按卡片 §4 五条逐条贴证据。
完成后停下报告，不要做前端。
```
