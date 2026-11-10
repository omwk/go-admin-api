# 后端接口清单与补充建议

按**功能模块的使用场景**盘点现有接口，标出已实现、建议补充、视情况补充。

- 盘点日期：2026-11-11（基线 `ff9c31c`）
- 事实来源：`api/openapi/*.yaml`（120 个接口）+ 路由注册代码
- 图例：✅ 已实现并已进统一 OpenAPI ｜ 🟡 已实现但未进统一 OpenAPI ｜ ⭐ 建议补充 ｜ 🔶 视情况

> 「已实现」指代码可运行、有集成测试；不代表有前端页面。页面缺口见 §9。

---

## 1. 汇总

| 分类 | 数量 |
|---|---:|
| ✅ 已实现且已文档化 | 120 |
| 🟡 已实现但未进统一 spec | 21（gateway 管理 10、gateway 开放 5、钱包 6） |
| ⭐ 建议补充（前端必需） | 12 |
| 🔶 视情况补充 | 9 |
| 明确不做 | 4 |

**结论**：后端能力完整度高，问题集中在两处——**资源回读缺失**（有写无读）与 **spec 未统一**（gateway/钱包不在 `api/openapi`）。

---

## 2. 身份与权限（identity）

### 认证

| 用途 | 接口 | 状态 |
|---|---|---|
| 登录 | `POST /auth/login` | ✅ |
| 刷新令牌 | `POST /auth/refresh` | ✅ |
| 注销当前会话族 | `POST /auth/logout` | ✅ |
| 二次认证（PII 前） | `POST /auth/reauth` | ✅ |
| 当前用户信息 | `GET /auth/me` | ✅ |
| 菜单树 + 权限码 | `GET /auth/menus` | ✅ |

### 用户管理

| 用途 | 接口 | 状态 | 说明 |
|---|---|---|---|
| 用户列表 | `GET /admin/users` | ✅ | 支持 keyword/status/sort（BE-1） |
| 新建用户 | `POST /admin/users` | ✅ | |
| 更新用户 | `PUT /admin/users/{id}` | ✅ | |
| 删除用户 | `DELETE /admin/users/{id}` | ✅ | |
| 重置密码 | `POST /admin/users/{id}/reset-password` | ✅ | |
| 分配角色 | `PUT /admin/users/{id}/roles` | ✅ | |
| **用户详情** | `GET /admin/users/{id}` | ⭐ | 编辑页需要回填，目前只能从列表取 |
| **已分配角色回读** | `GET /admin/users/{id}/roles` | ⭐ | 只有写没有读，角色勾选框无法回填 |

### 角色管理

| 用途 | 接口 | 状态 | 说明 |
|---|---|---|---|
| 角色列表 | `GET /admin/roles` | ✅ | |
| 新建 / 更新 / 删除 | `POST/PUT/DELETE /admin/roles[/{id}]` | ✅ | |
| 分配菜单 | `PUT /admin/roles/{id}/menus` | ✅ | |
| 分配权限 | `PUT /admin/roles/{id}/permissions` | ✅ | |
| 分配数据范围部门 | `PUT /admin/roles/{id}/depts` | ✅ | |
| **角色详情** | `GET /admin/roles/{id}` | ⭐ | |
| **菜单/权限/部门回读** | `GET /admin/roles/{id}/menus\|permissions\|depts` | ⭐ | 授权页打开时不知道当前勾选 |

### 菜单与权限

| 用途 | 接口 | 状态 |
|---|---|---|
| 菜单树 | `GET /admin/menus` | ✅ |
| 新建 / 更新 / 删除菜单 | `POST/PUT/DELETE /admin/menus[/{id}]` | ✅ |
| 权限码字典 | `GET /admin/permissions` | ✅ |
| **部门树** | `GET /admin/depts` | ⭐ |

> 部门树缺失是硬伤：`POST /admin/users` 必填 `dept_id`，但前端没有任何接口能列出可选的部门。

### 会话

| 用途 | 接口 | 状态 |
|---|---|---|
| 在线会话列表 | `GET /admin/sessions` | ✅ |
| 强制下线 | `DELETE /admin/sessions/{sid}` | ✅ |

---

## 3. 店铺与凭据（shop）

| 用途 | 接口 | 状态 | 说明 |
|---|---|---|---|
| 平台列表 | `GET /admin/platforms` | ✅ | 无权限码（公开字典） |
| 店铺列表 / 详情 | `GET /admin/shops[/{id}]` | ✅ | |
| 新建 / 更新 / 删除 | `POST/PUT/DELETE /admin/shops[/{id}]` | ✅ | |
| 店铺凭据列表 | `GET /admin/shops/{id}/credentials` | ✅ | |
| 新增凭据 | `POST /admin/shops/{id}/credentials` | ✅ | |
| 轮换凭据 | `PUT /admin/credentials/{cid}` | ✅ | |
| 检查凭据 | `POST /admin/credentials/{cid}/check` | ✅ | |
| 停用凭据 | `POST /admin/credentials/{cid}/disable` | ✅ | |
| 凭据审计 | `GET /admin/credentials/{cid}/audits` | ✅ | |
| 文件上传 / 下载 | `POST /admin/files`、`GET /admin/files/{id}` | ✅ | |
| **凭据版本历史** | `GET /admin/credentials/{cid}/versions` | ⭐ | M7 保留 90 天历史，但无接口可查 |
| **手动触发刷新** | `POST /admin/credentials/{cid}/refresh` | 🔶 | M7b 称有「手动入口」，但路由注册中未找到；需确认 |

---

## 4. 同步（sync）

| 用途 | 接口 | 状态 | 说明 |
|---|---|---|---|
| 触发同步 | `POST /admin/sync-jobs/{id}/run` | ✅ | |
| 运行记录列表 / 详情 | `GET /admin/sync-runs[/{id}]` | ✅ | |
| 死信列表 / 重放 | `GET /admin/dead-letters`、`POST .../replay` | ✅ | |
| 隔离区列表 / 重放 | `GET /admin/quarantines`、`POST .../replay` | ✅ | |
| 字段漂移 | `GET /admin/schema-drifts` + ack/resolve/ignore | ✅ | |
| 基线提升 | `POST /admin/schema-baselines/{id}/promote` | ✅ | |
| 历史重放 | `POST/GET /admin/replays`、`GET /{id}`、`POST /{id}/cancel` | ✅ | |
| **同步任务列表** | `GET /admin/sync-jobs` | ⭐ | 只能按 id 触发，前端无法列出有哪些任务 |
| **任务详情** | `GET /admin/sync-jobs/{id}` | ⭐ | 计划表达式、状态、当前游标 |

---

## 5. 商品与类目（catalog）

| 用途 | 接口 | 状态 |
|---|---|---|
| 商品列表 | `GET /admin/products` | ✅ |
| 商品详情 | `GET /admin/products/{id}` | ✅ |
| 新建 / 更新 / 删除 | `POST/PUT/DELETE /admin/products[/{id}]` | ✅ |
| 归并 listing | `POST /admin/products/{id}/merge` | ✅ |
| 商品关联 listing | `GET /admin/products/{id}/listings` | ✅ |
| 平台商品列表 | `GET /admin/listings` | ✅ |
| 类目树 | `GET /admin/categories` | ✅ |
| 类目映射列表 / 更新 / 自动匹配 | `GET /admin/category-mappings`、`PUT /{id}`、`POST /{id}` | ✅ |
| 库存查询 | `GET /admin/inventories` | ✅ |
| 导出触发 / 按 id 查询 | `POST /admin/products/export`、`GET /admin/exports/{id}` | ✅ |
| **导出任务列表** | `GET /admin/exports` | ⭐ |
| 类目新建/编辑/删除 | `POST/PUT/DELETE /admin/categories[/{id}]` | 🔶 |

> 商品编辑已有接口，但前端未做页面（见 §9）。

---

## 6. 订单（order）

| 用途 | 接口 | 状态 |
|---|---|---|
| 订单列表 | `GET /admin/orders` | ✅ |
| 订单详情 | `GET /admin/orders/{id}` | ✅ |
| 收货信息（需重认证） | `GET /admin/orders/{id}/address` | ✅ |
| 统计聚合 | `GET /admin/orders/stats` | ✅ |
| 导出触发 / 按 id 查询 | `POST /admin/orders/export`、`GET /admin/orders/exports/{id}` | ✅ |
| **导出任务列表** | `GET /admin/orders/exports` | ⭐ |
| 修改订单状态 / 备注 / 发货 | — | ❌ 明确不做 |

> 订单是本系统的**事实镜像**，不产生业务动作。改状态、发货属于上游平台或独立 OMS，不在本系统职责内。

---

## 7. 分享与报价（share）

| 用途 | 接口 | 状态 |
|---|---|---|
| 客户列表 / 详情 / 增删改 | `GET/POST/PUT/DELETE /admin/customers[/{id}]` | ✅ |
| 价目表列表 / 详情 / 增删改 | `GET/POST/PUT/DELETE /admin/price-lists[/{id}]` | ✅ |
| 价目表条目维护 | `POST /admin/price-lists/{id}` | ✅ |
| 价格预览 | `POST /admin/price-preview` | ✅ |
| 分享链接列表 / 创建 | `GET/POST /admin/share-links` | ✅ |
| 重新生成 / 撤销 | `POST /admin/share-links/{id}/regenerate\|revoke` | ✅ |
| 访问记录 | `GET /admin/share-links/{id}/visits` | ✅ |
| 询价列表 / 更新 | `GET/PUT /admin/inquiries[/{id}]` | ✅ |
| 询价 PII（重认证） | `GET /admin/inquiries/{id}/pii` | ✅ |
| 询价导出 | `GET /admin/inquiries/export` | ✅ |
| 匿名：根准入 / 验密 / 详情 / 询价 | `GET /share/{token}` 等 4 个 | ✅ |
| 分享链接详情 | `GET /admin/share-links/{id}` | 🔶 |

---

## 8. 计费与开放网关

### 计费（billing）

| 用途 | 接口 | 状态 |
|---|---|---|
| 定价方案 | `GET/POST/PUT /admin/pricing-plans[/{id}]` | ✅ |
| 订阅（发放套餐） | `POST /admin/subscriptions` | ✅ |
| 权益列表 / 单位流水 | `GET /admin/entitlements[/{id}/txns]` | ✅ |
| 对账触发 / 差异 / 补偿 | `POST /admin/billing-reconciliations`、`GET /admin/reconcile-diffs[/{id}/compensations]` | ✅ |
| 用量查询 | `GET /admin/billing-usage` | ✅ |
| 账单列表 / 详情 / 生成 / 发行 | `GET /admin/billing-statements[/{id}]`、`POST ...:generate\|{id}/issue` | ✅ |
| 调整单 | `GET/POST /admin/billing-adjustments[/{id}/apply]` | ✅ |
| 钱包列表 / 详情 / 流水 | `/admin/wallets[/{id}]`、`/{id}/txns` | 🟡 未进 `billing.yaml` |
| 建钱包 / 充值 | `POST /admin/wallets`、`/{id}/recharge` | 🟡 |
| 兑换码 | `POST /admin/recharge-codes` | 🟡 |
| 钱包流水按时间筛选 | 参数补充 | 🔶 |

### 开放网关（gateway）

| 用途 | 接口 | 状态 |
|---|---|---|
| 应用管理 | `GET/POST /admin/api-clients`、`PUT /{id}` | 🟡 |
| 密钥列表 / 签发 / 吊销 | `GET/POST /admin/api-clients/{id}/secrets`、`DELETE .../{sid}` | 🟡 |
| 路由授权 | `POST /admin/api-clients/{id}/routes` | 🟡 |
| 订单级授权 | `POST/DELETE /admin/api-clients/{id}/orders[/{gid}]` | 🟡 |
| 路由登记 | `GET/POST /admin/api-routes`、`PUT /{id}` | 🟡 |
| 对外：订单状态 | `GET /open/v1/orders/{id}/status` | 🟡（模块内 spec） |
| 对外：余额 / 兑换 | `GET /open/v1/balance`、`POST /open/v1/recharge-code` | 🟡 |
| 对外：调用结果 / 用量 | `GET /open/v1/calls/{call_id}/result`、`GET /open/v1/usage` | 🟡 |

> 🟡 的两组都有集成测试，但没有进 `api/openapi/`。**前端无法用统一方式生成类型**，这是当前最该补的文档债。

---

## 9. 前后端对照：页面缺口

| 模块 | 后端接口 | 前端页面 | 差距 |
|---|---|---|---|
| 身份/用户 | 6 | 列表（只读） | **缺增删改、分配角色** |
| 角色 | 7 | 无 | **整页缺** |
| 菜单 | 4 | 无 | **整页缺** |
| 会话 | 2 | 无 | 缺 |
| 店铺/凭据 | 12 | 无 | 整页缺 |
| 同步 | 16 | 无 | 整页缺 |
| 商品 | 15 | 列表+详情（进行中） | 缺编辑、归并 |
| 订单 | 6 | 无 | 整页缺 |
| 分享 | 25 | 无 | 整页缺 |
| 计费 | 24 | 无 | 整页缺 |
| 网关 | 15 | 无 | 整页缺 |

**这是「后端 80%、可用性 10%」的具体体现**：接口在，页面不在。

---

## 10. 建议补充（⭐）汇总

| # | 接口 | 为什么必须 | 人日 |
|---|---|---|---:|
| 1 | `GET /admin/depts` | 建用户必填 dept_id，无接口可选 | 0.3 |
| 2 | `GET /admin/users/{id}` | 编辑页回填 | 0.2 |
| 3 | `GET /admin/users/{id}/roles` | 角色勾选回显 | 0.2 |
| 4 | `GET /admin/roles/{id}` | 角色编辑回填 | 0.2 |
| 5 | `GET /admin/roles/{id}/menus` | 菜单勾选回显 | 0.2 |
| 6 | `GET /admin/roles/{id}/permissions` | 权限勾选回显 | 0.2 |
| 7 | `GET /admin/roles/{id}/depts` | 数据范围回显 | 0.2 |
| 8 | `GET /admin/sync-jobs` | 任务列表页 | 0.3 |
| 9 | `GET /admin/sync-jobs/{id}` | 任务详情 | 0.2 |
| 10 | `GET /admin/exports`（商品） | 导出任务列表 | 0.2 |
| 11 | `GET /admin/orders/exports` | 同上 | 0.2 |
| 12 | `GET /admin/credentials/{cid}/versions` | 凭据历史（M7 已存数据） | 0.3 |
| **合计** | | | **约 3 人日** |

都是**读接口**，不改表、不改事务、不加锁，风险低。

---

## 11. 视情况补充（🔶）

| 接口/能力 | 何时需要 | 理由 |
|---|---|---|
| `POST/PUT/DELETE /admin/categories` | 需要运营自建类目 | 当前类目来自平台同步；手工建类目会与同步冲突，需先定归属 |
| `POST /admin/credentials/{cid}/refresh` | 需要人工强制刷新 | M7b 声称有手动入口但路由未找到，先确认真实情况 |
| `GET /admin/share-links/{id}` | 链接详情页 | 列表已含主要字段，详情页可后置 |
| 钱包流水的日期/类型筛选 | 对账排查 | 数据量小时不必要 |
| 导出任务统一列表 | 导出页 | 可按域分别实现，也可统一 `/admin/exports?kind=` |
| 审计日志查询 | 合规需求 | 表已存在，接口未建；先确认谁来查、查多久 |
| 仪表盘聚合 `GET /admin/dashboard` | 首页看板 | 可用现有 stats 拼装，不必新增 |
| 批量用户操作（禁用/改部门） | 组织调整 | 单人操作够用时不需要 |
| 菜单排序批量接口 | 菜单管理体验 | 逐个更新可接受 |

---

## 12. 明确不做（❌）

| 能力 | 理由 |
|---|---|
| 订单改状态 / 发货 / 备注 | 订单是事实镜像，业务动作属于上游平台或独立 OMS |
| 前端代发分享密码 | 密码只应存在于创建时的一次性回执，不落库不重发 |
| 客户 PII 导出 | ADR-020 明确禁止，改为站内待办 |
| 绕过重认证读取 PII | ADR-019 明确要求 5 分钟内重认证 + 逐次审计 |

---

## 13. 建议的补充顺序

```
P0（3 人日）：§10 全部 —— 前端系统管理页的前置
P1（确认后）：credentials/{cid}/refresh 是否已存在
P2：视情况项按页面需求逐条批准
```

补充完 P0 后，角色/用户/菜单三个页面才能真正做出来；这正是 FE-1.5 的依赖。
