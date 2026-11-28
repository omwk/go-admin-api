# 对内 API 接入 · 设计与分期

- 日期：2026-11-28
- 状态：待确认
- 前置：对外网关（`api_clients` / `api_routes` / 计费 / 观测）已完成并合并
- 范围：**我们调别人**——定义外部 API、在线调试、调用日志、被同步引擎引用

---

## 0. 事实核对（AGENTS §13）

| 断言 | 命令 | 结果 |
|---|---|---|
| 平台适配器只有 Shopee | `Select-String sync_runtime.go -Pattern 'platforms.Register'` | `mock.New` + `shopee.New` 两处 |
| platform 表有 3 条 | `select code,name,auth_type,status from platform` | shopee/taobao/shopify，均 active |
| 同步资源 4 类 | `Select-String mapping_types.go` | product / order / inventory / category |
| **无通用 API 定义表** | `information_schema` 搜索 integration/endpoint/http_api | 0 行 |
| 店铺凭据相关表 10 张 | `information_schema` 搜索 credential | shop_credential、credential_version 等 |

结论：**平台是「代码适配器 + 元数据表」的混合模式；没有可配置的通用 HTTP 接入能力。**

---

## 1. 问题

现在要接一个新平台（例如某小众 ERP），必须：

```
1. 写 Go 适配器（签名、分页、错误码）
2. 注册到 connector registry
3. 发版部署
```

而大多数对接只是「带 API Key 的 REST + 分页」。这类工作不该每次都改代码。

同时缺三样东西：

| 缺失 | 后果 |
|---|---|
| API 定义（base URL、认证、超时） | 只能硬编码在适配器里 |
| 端点定义（方法、路径、参数） | 无法在界面查看有哪些可用接口 |
| 在线调试 | 只能写临时脚本验证 |
| 调用日志 | 出问题只能翻应用日志 |

---

## 2. 方案对比

### 方案 A：全部改为可配置（去代码适配器）

```
所有平台 = API 定义 + 端点 + 字段映射
```

- 优点：零发版接新平台
- 缺点：Shopee 这类需要签名/分页/限流/重试策略的平台，用通用配置描述会变成一门 DSL；
  现有 3 个平台适配器要重写；测试与故障定位难度大幅上升

### 方案 B：双层——代码适配器 + 可配置 REST

```
复杂平台（Shopee/TikTok） → 代码适配器（保留）
简单 REST               → 可配置定义（新增）
两者都注册到 connector registry，同步引擎无感知
```

- 优点：不动现有实现；80% 的简单对接零发版
- 缺点：两套并存，需要统一的接口与文档

### 方案 C：只做调试台，不改接入方式

```
提供一个「HTTP 调试器」+ 日志，定义仍写代码
```

- 优点：最小改动
- 缺点：没解决「接新平台要发版」的核心痛点

### 结论

**采用方案 B。** A 的成本被低估（签名/分页/限流要抽象成配置），C 不解决问题。

---

## 3. 目标模型

```
integration（API 定义）
├ 基础：name、base_url、认证方式、超时、重试、限流
├ 端点 endpoint：method、path、query/body 模板、示例
├ 调试：按端点发真实请求，看响应与耗时（不落库）
└ 调用日志：谁调的、状态码、耗时、大小（默认不留 body）

sync 引擎：把「可配置定义 + 端点」包装成 Platform 适配器
```

### 3.1 认证方式

| 类型 | 配置 |
|---|---|
| `none` | — |
| `api_key` | 位置（header/query）、名称、值（加密） |
| `bearer` | token（加密） |
| `basic` | 用户名 + 密码（加密） |
| `oauth2_client` | token URL、client id/secret、scope（加密） |

凭据存放在**接入定义级**（一个 API 一套），不按店铺；
店铺级凭据（`shop_credential`）继续服务需要每店授权的平台（如 Shopee OAuth）。

> 这条是本次需要你确认的关键设计点：如果某 API 是「每个店铺一个 key」，
> 应走店铺凭据而不是接入定义凭据。

### 3.2 安全边界

- `base_url` 必须是 https 公网地址；禁内网/回环/元数据地址（复用 SSRF 校验）
- 调试只能请求已保存定义下的端点，不允许任意 URL
- 响应体默认不落库；调试页展示后即丢弃，日志只记状态码/耗时/大小
- 凭据加密存储，接口返回掩码；查看明文需二次确认（同密钥查看的限流与审计）

---

## 4. 数据模型

```sql
integration
  id, tenant_id, name, base_url, auth_type,
  auth_config_enc bytea,          -- 密钥加密
  timeout_ms, retry_max, qps_limit,
  status, created_by, created_at, updated_at

integration_endpoint
  id, tenant_id, integration_id, code, name,
  method, path, query_template jsonb, body_template jsonb,
  sample_response jsonb,          -- 仅文档用途
  status, created_at, updated_at

integration_call_log
  id, tenant_id, integration_id, endpoint_id,
  status_code, latency_ms, req_bytes, resp_bytes,
  error_code, trace_id, called_at, caller (sync/manual)
```

约束：

- `(tenant_id, integration_id, code)` 唯一
- 删除 integration 前必须先删端点（或级联软删）
- `base_url` 变更需重新触发一次连通性检查

## 5. 接口

```
GET/POST/PUT/DELETE /admin/integrations
GET/POST/PUT/DELETE /admin/integrations/{id}/endpoints
POST /admin/integrations/{id}/test            连通性（HEAD base_url）
POST /admin/integrations/{id}/endpoints/{eid}/debug
     { query?, body? } → { status_code, latency_ms, response(截断), truncated }
GET  /admin/integration-calls?integration_id=&status=&from=&to=&page=
```

权限：`integration:manage`、`integration:debug`、`integration:call:read`（新增并 seed 超管）。

## 6. 与同步引擎的衔接（第二阶段）

```
定义 integration + endpoint(order.list)
  ↓
sync 引擎按 resource=order、platform=<integration code> 调用
  ↓
分页/游标由端点模板声明（next_cursor_path）
  ↓
写 ingestion 原始报文 → mapping profile → 业务表
```

第一阶段只做到「调试 + 日志」；真正接入同步放第二阶段，避免一次改太多。

## 7. 分期与工作量

| 阶段 | 内容 | 人日 |
|---|---|---|
| 一 | 表 + CRUD + 凭据加密 + 连通性测试 | 2 |
| 二 | 端点点定义 + 调试 + 调用日志 | 2 |
| 三 | 接入同步引擎（分页/游标、映射绑定） | 3 |
| 四 | 前端三页（接入列表/端点编辑/调试与日志） | 3 |

## 8. 已确认的决策（2026-11-28）

| 问题 | 决定 |
|---|---|
| 凭据归属 | **接入定义级**（一个 API 一套）。店铺级需求走现有 `shop_credential` |
| 分页模型 | **cursor + offset 两种**，端点表预留 `cursor_path` / `offset_param` 字段 |
| 调试权限 | **单一 `integration:debug` 权限**，不额外做二次确认 |
| 导入 OpenAPI | **暂不做**，等真实接入第二个平台时再评估 |

## 8.1 原始问题（存档）

1. **凭据归属**：接入定义级（一 API 一套）够用吗？是否有「每店铺一个 key」的需求？
2. **分页模型**：只支持 cursor + offset 两种，还是需要自定义表达式？
3. **调试权限**：调试会真实调用外部 API（可能产生费用/副作用），是否需要单独权限与二次确认？
4. **是否要做「导入 OpenAPI/Swagger」**：能从文档自动生成端点，可省大量手工输入，但解析器工作量 +2 天。

---

## 9. 不在本期

- GraphQL / SOAP
- Webhook 回调接收
- 流量回放与录制
- 多环境（sandbox/prod）切换
