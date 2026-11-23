# BE-IDN-030 · 开放平台菜单与初始路由

- ID: BE-IDN-030
- 类型: 新增
- 仓库: 后端
- 状态: 待实现
- 预计: 0.3 人日
- 分支: `feat/be-idn-030-gateway-menus`
- 依赖: BE-IDN-029（观测接口已合并）

---

## 1. 问题

前端要建「开放平台」控制台，但后端没有对应菜单，侧边栏无法进入；
且 `api_route` 初始为空，试调功能没有任何可调的路由。

## 2. 改动

### 2.1 菜单

新增一级目录与四个页面（component 用简写，与 FE-IDN-018 一致）：

```
开放平台 (dir, /open-platform, sort 5)
├ API 客户端  /open-platform/clients   open-platform/clients/index  gateway:client:list   sort 1
├ 路由        /open-platform/routes    open-platform/routes/index   gateway:route:list    sort 2
├ 调用日志    /open-platform/calls     open-platform/calls/index    gateway:call:read     sort 3
└ 用量统计    /open-platform/usage     open-platform/usage/index    gateway:call:read     sort 4
```

- 写入 `seed_pages` 覆盖守卫清单
- 权限码已存在（BE-IDN-029 补齐），无需新增

### 2.2 初始路由（seed）

插入两条内置路由（`tenant_id = 平台租户`）：

| code | name | method | path | visibility | billing_mode | capability |
|---|---|---|---|---|---|---|
| `usage.read` | 用量查询 | GET | `/open/v1/usage` | public | free | `usage.read` |

- 幂等：按 `code` upsert，已存在不覆盖租户修改
- 只 seed 这一条，其他路由由管理员在界面添加

### 2.3 文档

`docs/design/api-gateway-console.md` 补一节「如何新增一个对外 API」：

```
1. 后端实现 /open/v1/xxx 处理器并注册
2. 管理端「路由」新建：code、方法、路径、计费模式
3. 给客户端授权并设配额
4. 用「试调」验证
```

## 3. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 菜单可见 | 超管 `/auth/menus` 含「开放平台」下四项 |
| 2 | 权限过滤 | 无 `gateway:route:list` 的账号看不到路由菜单 |
| 3 | 覆盖守卫 | 从 seed_pages 删除任一菜单 → 测试失败 |
| 4 | 初始路由 | 新库 seed 后 `api_route` 含 `usage.read`；重复 seed 不重复 |
| 5 | 可试调 | 用「试调」调用 `usage.read` 成功返回用量 |
| 6 | 全量 | `verify` 通过 |

## 4. Codex prompt

```
实现 docs/tasks/BE-IDN-030-gateway-menus.md。

分支：feat/be-idn-030-gateway-menus（从最新 main d2531bb 开）

必读：本卡、internal/modules/identity/seed_catalog.go、
seed_pages_test.go、internal/modules/gateway/admin_http.go（route 字段）、
migrations/（api_route）

改动：
1. 新增「开放平台」目录与四个页面菜单（简写 component），写入覆盖清单
2. seed 一条内置路由 usage.read（GET /open/v1/usage，public，free），幂等
3. 设计文档补「如何新增一个对外 API」
4. OpenAPI 无变化

约束：
- 不硬编码租户 ID，平台租户从配置读取
- 不覆盖租户对内置路由的修改（已存在则跳过）

验收按卡片 §3 六条逐条贴证据；第 5 条贴试调成功响应。
完成后停下报告，不要做前端。
```
