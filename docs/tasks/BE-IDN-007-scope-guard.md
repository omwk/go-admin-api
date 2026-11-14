# BE-IDN-007 · 超管范围守卫与同类站点排查

- ID: BE-IDN-007
- 类型: 修复
- 仓库: 后端
- 状态: 待实现
- 预计: 1 人日
- 分支: `feat/be-idn-007-scope-guard`
- 依赖: BE-IDN-006（超管短路，已合并）

---

## 1. 背景

BE-IDN-006 修掉了一个具体现象（无角色超管收件箱 20108），但那只是**一个调用点**。
同类失败可能在别的模块仍然存在，而现有测试没有覆盖「超管 + 无角色」这个组合。

更根本的问题是：**没有任何守卫**阻止新增一个「依赖角色才能解析范围」的接口。
下次谁再写一个 scope 依赖，同样会静默失败，只在某个新租户里暴露。

## 2. 目标

1. **矩阵回归**：超管无角色时，跨模块的范围相关接口都不得返回 20108
2. **结构性守卫**：新增一个返回 20108 的位置，必须登记，否则测试失败
3. 修掉矩阵暴露的任何真实缺陷

## 3. 改动

### 3.1 矩阵回归测试

新增 `TestSuperAdminWithoutRoleAcrossModules`（集成）：

构造一个 `is_super=true`、无任何角色的用户，逐个调用：

| 模块 | 接口 | 期望 |
|---|---|---|
| 订单 | `GET /admin/orders` | 200 |
| 订单导出 | `POST /admin/orders/export` | 201 |
| 店铺 | `GET /admin/shops` | 200 |
| 店铺更新 | `PUT /admin/shops/{id}` | 200 |
| 凭据 | `GET /admin/shops/{id}/credentials` | 200 |
| 凭据审计 | `GET /admin/credentials/{cid}/audits` | 200 |
| 客户 | `GET /admin/customers` | 200 |
| 询盘 | `GET /admin/inquiries` | 200 |
| 账单 | `GET /admin/billing-statements` | 200 |
| 同步 | `GET /admin/sync-runs` | 200 |
| 商品 | `GET /admin/products` | 200 |
| 通知 | `GET /admin/notifications` | 200 |

断言：**任何一个返回 403/20108 都算失败**（权限类 403/20107 也不应出现，因为超管绕过权限）。

同时补一个反例：

```
同样接口，用「有 system:* 权限但数据范围为 self 且无店铺」的普通用户
→ 200 + 空列表（不是 403/20108）
```

### 3.2 结构性守卫

新增 `TestDataScopeDenialSitesAreRegistered`（单元）：

- 扫描 `internal/` 下所有非测试 Go 文件，找出返回 `errs.ErrDataScopeDenied` 的位置
- 每处必须在 `docs/design/data-scope-sites.md` 的表里登记，字段：文件、函数、触发条件、超管是否会命中
- 未登记 → 测试失败，报出文件与行号

同时该文档记录当前的 11 处站点，逐条说明「超管能否走到这里」。若某处超管可达且会失败，标为待修。

> 这是把 S1b 的「投影一致性」思路搬到范围检查上：**让遗漏变成测试失败，而不是线上事故**。

### 3.3 修复

矩阵或登记表暴露的真实问题在本卡修复（预计主要是：
`order_export.go:88` 的快照店铺校验、`shop_scope.go` 的 `ValidateAssignment`）。
修复方式优先「超管短路」，其次「依赖 Authorize 产出的 all 范围」，避免各处重复判断。

## 4. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 矩阵-超管 | 12 个接口全部非 20108、非 20107 |
| 2 | 矩阵-普通用户 | self 范围无店铺 → 200 空列表 |
| 3 | 登记表 | `data-scope-sites.md` 覆盖全部 20108 站点 |
| 4 | 守卫生效 | 临时新增一个 20108 返回点不登记 → 测试失败 |
| 5 | 真实修复 | 矩阵中曾失败的接口在修复后通过（贴修复前后对比） |
| 6 | 非超管语义不变 | 现有 scope 测试全部通过 |
| 7 | 全量 | `verify` 通过 |

## 5. 不做

- 重构 scope 体系
- 给非超管自动补角色
- 前端改动

## 6. Codex prompt

```
实现 docs/tasks/BE-IDN-007-scope-guard.md。

分支：feat/be-idn-007-scope-guard（从最新 main 开）

必读：本卡、internal/bootstrap/identity_scope.go（Authorize 超管短路）、
internal/bootstrap/shop_scope.go、order_export.go、
internal/kernel/scope/、docs/tasks/BE-IDN-006-super-scope.md

改动：
1. 新增矩阵回归测试：is_super 无角色用户跨 12 个接口均不得 20108/20107；
   另加普通用户 self 范围无店铺 → 200 空列表
2. 新增 data-scope-sites.md 登记表 + 扫描测试：任何返回 ErrDataScopeDenied
   的位置必须登记，否则测试失败
3. 修复矩阵暴露的问题（优先超管短路）

验收按卡片 §4 七条逐条贴证据；第 5 条要贴修复前后对比。
完成后停下报告，不要做前端。
```
