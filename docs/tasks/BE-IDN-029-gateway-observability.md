# BE-IDN-029 · 网关日志、统计与试调

- ID: BE-IDN-029
- 类型: 新增
- 仓库: 后端
- 状态: 已实现，待评审
- 预计: 1 人日
- 分支: `feat/be-idn-029-gateway-observability`
- 依赖: 无
- 依据: `docs/design/api-gateway-console.md`

---

## 1. 背景

对外网关的后端（客户端/密钥/路由/授权/配额/计费）已完成，但没有管理端观测能力：

- `api_call_log` 有数据但无查询接口
- 无用量与成功率统计
- 无法在线试调，只能外部 curl

## 2. 改动

### 2.1 调用日志查询

```
GET /admin/api-calls
  ?client_id=&route_id=&status=success|failed&from=&to=&trace_id=&page=&page_size=
```

- 默认最近 24 小时；范围最大 30 天
- 返回：客户端名、路由 code、方法、路径、状态码、耗时、计费模式、费用、错误码、时间、trace_id
- **不含请求体/响应体**（隐私）
- 租户隔离；平台管理员可跨租户

### 2.2 统计

```
GET /admin/api-calls/summary?from=&to=&group_by=client|route|day
→ { total_calls, items:[{ key, calls, success_rate, p95_latency_ms, cost_cents }] }
```

- 成功率 = 2xx / 全部
- P95 用近似分位（PostgreSQL `percentile_cont`）
- 只统计 `state=finished` 的调用

### 2.3 在线试调

```
POST /admin/api-clients/{id}/try
{
  "route_id": 3,
  "query": {"page": "1"},
  "body": null
}
→ { "status_code": 200, "latency_ms": 42, "response": {...},
    "billing": { "mode": "metered", "units": 1, "billed": false } }
```

要求：

- 走**真实鉴权链**（签名、配额、IP 白名单、路由状态）
- `test=true`：不写计费流水，但写调用日志（`charge_source=manual_test`）
- 响应体截断 256 KB
- 只能试调该客户端已授权且 `visibility=public` 的路由
- 失败也返回结构化结果（status_code + error_code），不抛 500
- 权限沿用 `gateway:client:manage`

### 2.4 权限码

检查现有 `gateway:*` 权限码是否覆盖：
`gateway:client:list/create/manage`、`gateway:route:list/create/update`、`gateway:call:read`。
缺的补上并 seed 给内置超管。

## 3. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 日志列表 | 按客户端/路由/状态/时间筛选正确；分页与总数一致 |
| 2 | 时间范围 | 默认 24h；超 30 天返回 400 |
| 3 | trace 查询 | 用 trace_id 精确命中 |
| 4 | 隐私 | 响应无请求体/响应体 |
| 5 | 统计 | 按 client/route/day 聚合；成功率与 P95 与手算一致 |
| 6 | 试调成功 | 200，返回响应与耗时；日志新增一条 `manual_test` |
| 7 | 试调不计费 | `api_call_meter` 无新增 |
| 8 | 试调未授权 | 未授权路由 → 403 结构化结果 |
| 9 | 试调截断 | 大响应 >256KB 被截断并标记 |
| 10 | 租户隔离 | 其他租户客户端不可见/不可试调 → 404 |
| 11 | 契约 | OpenAPI 更新，双向一致 |
| 12 | 全量 | `verify` 通过 |

## 4. 不做

- 请求/响应体留存
- 实时流式日志
- 告警规则（后续）

## 验收记录

- 2026-11-23：后端已实现，逐项证据见 [十二项验收记录](evidence/BE-IDN-029/acceptance.md)。
- `scripts/verify.ps1 -Integration`：`all checks passed`；专项测试与本地 race 检测通过；迁移 `17 -> 16 -> 40; dirty=false`。
- 已清理：专项验收使用独立测试数据库，`t.Cleanup` 执行 `DROP DATABASE IF EXISTS`；开发库无本卡测试数据，未改系统配置。

## 5. Codex prompt

```
实现 docs/tasks/BE-IDN-029-gateway-observability.md。

分支：feat/be-idn-029-gateway-observability（从最新 main 38be7dd 开）

必读：本卡、docs/design/api-gateway-console.md、
internal/modules/gateway/（admin_http.go、call.go、call_execute.go、admin_types.go）、
migrations/（api_call_log、api_call_meter）、
internal/modules/identity/permission/registry.go（gateway 权限码）

改动：
1. GET /admin/api-calls（筛选/分页/时间范围/trace）
2. GET /admin/api-calls/summary（client/route/day 聚合，成功率与 P95）
3. POST /admin/api-clients/{id}/try（真实鉴权链、test=true、不写计费、写日志）
4. 补齐 gateway 权限码并 seed 内置超管
5. OpenAPI 同步

约束：
- 不返回请求体/响应体
- 试调不得影响配额与计费（配额检查仍生效，但不扣减？见卡片 §2.3：走真实鉴权链但 test 标记 —— 实现时明确：配额检查生效、扣减跳过并记录）
- 租户隔离

验收按卡片 §3 十二条逐条贴证据：
- 第 5 条贴 SQL 手算对照
- 第 7 条贴 api_call_meter 前后计数
完成后停下报告，不要做前端。
```
