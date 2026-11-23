# API 网关控制台 · 现状与分阶段计划

- 日期：2026-11-23
- 决定：**两个方向都要，先对外后对内**
  1. 对外：别人调我们（`api_clients` / `api_routes` / 配额 / 计费 / 调用日志）
  2. 对内：我们调别人（平台/接口定义、调试、字段映射）
- 国外平台（Shopee 等）优先级最后，先不接真实业务

---

## 1. 现状盘点

### 1.1 对外网关（后端已有，缺界面与观测）

| 能力 | 后端 | 说明 |
|---|---|---|
| 客户端 | ✅ 列表 / 创建 / 更新 | `api_client`：app_id、状态、IP 白名单 |
| 密钥 | ✅ 列表 / 轮换 / 删除 | `api_client_secret`，支持双密钥过渡 |
| 路由 | ✅ 列表 / 创建 / 更新 | `api_route`：code、方法、路径、版本、计费模式、价格 |
| 授权 | ✅ 绑定路由 / 解绑 / 配额 | `api_client_route` + 每日/每月配额 |
| 签名 | ✅ v1 / v2 | 鉴权、防重放 |
| 调用执行 | ✅ | 配额预留、幂等、计费、结果指纹 |
| 用量查询 | ✅ 客户端侧 | `GET /open/v1/usage` |
| 计费与对账 | ✅ | `api_call_meter`、`open_billing`、`reconcile` |
| **管理端调用日志** | ❌ | 表 `api_call_log` 有数据，无查询接口 |
| **管理端用量统计** | ❌ | 无按客户端/路由/日聚合 |
| **在线试调** | ❌ | 只能在外部用 curl 调 |

### 1.2 对内接入（后端仅 Shopee 硬编码）

| 能力 | 现状 |
|---|---|
| 平台元数据 | `platform` 表 3 条（shopee/taobao/shopify），无 CRUD |
| 平台适配器 | 仅 Shopee 有代码；taobao/shopify 只有记录 |
| API 定义（任意 HTTP） | ❌ 无 |
| 在线调试 | ❌ 无 |
| 调用日志 | 仅 ingestion 原始报文，无「接口调用」视角 |
| 字段映射 | 有 mapping profile（商品/订单），但绑定平台代码 |

---

## 2. 阶段一：对外网关控制台

### 2.1 后端补三块

**A. 管理端调用日志查询**

```
GET /admin/api-calls
  ?client_id=&route_id=&status=success|failed&from=&to=&page=&page_size=
→ { list:[{ id, client_name, route_code, method, path, status_code,
            latency_ms, billing_mode, cost_units, error_code, called_at,
            trace_id, state }], total }
```

- 默认最近 24 小时；最多查 30 天
- 不含请求体/响应体（隐私）
- 支持按 trace_id 精确查

**B. 用量与质量统计**

```
GET /admin/api-calls/summary?from=&to=&group_by=client|route|day
→ { items:[{ key, calls, success_rate, p95_latency_ms, cost_cents }], total_calls }
```

**C. 在线试调**

```
POST /admin/api-clients/{id}/try
{ "route_id": 3, "method": "GET", "path": "/open/v1/orders",
  "query": {"page":"1"}, "body": null }
→ { "status_code": 200, "latency_ms": 42, "response": {...},
    "billing": { "mode": "metered", "units": 1, "billed": false } }
```

要求：

- 以该客户端的真实鉴权链执行（签名、配额、白名单都生效）
- 标记 `test=true`：**不写入计费**，但写入调用日志（`charge_source=manual_test`）
- 响应体截断 256KB，超长提示
- 只能试调已授权给该客户端且在 `visibility=public` 的路由
- 权限 `gateway:client:manage`（新增或复用）

### 2.2 前端控制台

```
API 网关
├ 客户端      列表 / 新建 / 启停 / IP 白名单 / 密钥轮换（一次性展示）
├ 路由        列表 / 新建 / 编辑 / 版本 / 计费模式与价格
├ 授权与配额  某客户端 → 勾选路由 + 每日/每月配额 + 过期时间
├ 调用日志    筛选（客户端/路由/状态/时间）+ 详情抽屉（trace_id、耗时、计费）
├ 统计        按客户端 / 路由 / 日期；成功率、P95、费用
└ 试调        选客户端 + 选路由 + 参数 → 实时响应与耗时
```

关键交互：

- 密钥创建后**只展示一次**，关闭即不可再取（现状后端如此）
- 路由 `code` 创建后不可改（被授权与日志引用）
- 配额 `0` 表示不限，UI 要说清
- 试调结果标注「测试调用，不计费」

## 3. 阶段二：对内 API 接入（后续设计）

先只定方向，不做卡片：

```
概念：API 定义（base URL + 认证 + 超时/重试）→ 端点（方法/路径/参数/响应映射）
      → 调试（发真实请求，看响应与耗时）→ 日志（调用记录、成功率）
      → 被业务模块引用（店铺同步、订单拉取）
```

与阶段一的关系：阶段一的签名/日志/重放等基础设施可复用（调用日志模型、trace、脱敏）。

## 4. 交付顺序

| 顺序 | 卡片 | 端 | 人日 |
|---|---|---|---|
| 1 | BE-IDN-029 网关日志/统计/试调 | 后端 | 1 |
| 2 | FE-IDN-020 网关管理控制台 | 前端 | 3 |
| 3 | （阶段二设计） | 文档 | 0.5 |
| 4 | 对内 API 接入 | 前后端 | 待估 |

## 5. 如何新增一个对外 API

1. 后端实现 `/open/v1/xxx` 处理器，在 `gateway.Capabilities()` 登记 code、方法、路径模板和路由分组，并注册到独立 open 分发链。数据库中的路由只能配置已实现的能力，不能转发到任意 URL。
2. 在管理端「开放平台 → 路由」新建路由，填写 code、capability、方法、路径、分组、可见性和计费模式；需试调的路由设为 `public` 并启用。收费路由同时设置价格、每次用量与币种。
3. 创建或选择 API 客户端，授权该路由并设置每日／每月配额及有效期；配额 `0` 表示不限。保留客户端的一次性密钥供外部签名调用。
4. 用该客户端的「试调」验证。试调通过真实签名、客户端状态、IP 白名单、路由授权和配额校验，返回响应及耗时；`test=true` 且不计费、不消耗配额，只记录调用元数据。

`go run ./cmd/cli seed` 会为 `tenant.platform_id`（环境变量 `CH_TENANT_PLATFORM_ID`）配置的平台租户补入内置路由 `usage.read`：`GET /open/v1/usage`、`public`、`free`、已启用。按 `(tenant_id, code)` 判断是否已存在，重复 seed 跳过并保留管理员修改。其他路由由管理员按上述步骤添加；seed 不会自动向客户端授权。
