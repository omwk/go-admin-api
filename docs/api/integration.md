# 接口集成规则

面向调用方（Web 管理后台、分享站、第三方应用）的接口约定。
完整字段定义见 `api/openapi/*.yaml`；本文件只写**跨域通用规则**与**前端接入注意事项**。

---

## 1. 基础

| 项 | 约定 |
|---|---|
| 基路径 | `/api/v1` |
| 请求体 | `application/json; charset=utf-8` |
| 时间格式 | RFC3339 UTC（`2026-11-10T08:00:00Z`） |
| 金额单位 | `int64` 分（**禁止浮点运算**） |
| 响应编码 | UTF-8 |

## 2. 响应信封

成功与失败使用同一外壳：

```json
{
  "code": 0,
  "message": "ok",
  "trace_id": "…",
  "request_id": "…",
  "data": { }
}
```

- `code = 0` 表示成功，非 0 为业务错误码
- `trace_id` 服务端生成，用于日志关联
- `request_id` 可回显客户端传入的 `X-Request-Id`（仅观测用，**不能用于鉴权或计费**）
- **不要直接展示 `message`**：按 `code` 映射本地文案，后端文案可能调整
- HTTP 状态码同时表达语义（4xx/5xx），不要只看 `code`

## 3. 认证

| 接口 | 作用 |
|---|---|
| `POST /api/v1/auth/login` | 用户名密码登录，返回 `TokenPair` |
| `POST /api/v1/auth/refresh` | 用 refresh 换新 pair |
| `POST /api/v1/auth/logout` | 注销当前会话族 |
| `POST /api/v1/auth/reauth` | 二次认证（PII 查看前，5 分钟有效） |
| `GET  /api/v1/auth/me` | 当前用户与角色 |
| `GET  /api/v1/auth/menus` | 菜单树 + 权限码列表 |

`TokenPair`：

```json
{ "access_token": "…", "refresh_token": "…", "token_type": "Bearer", "expires_in": 900 }
```

- `access_token`：HS256 JWT，**15 分钟**
- `refresh_token`：32 字节随机串（base64url），**7 天**
- 刷新是**轮换制**：旧 refresh 用后即失效

### 前端存储建议

| 令牌 | 建议位置 | 理由 |
|---|---|---|
| access | 内存（模块变量 / Pinia） | 生命周期短，刷新页面重新获取 |
| refresh | `sessionStorage` | 关闭标签页即失效；**接受 XSS 风险**（已在 ADR-024 记录） |

### 并发刷新（必须处理）

多个请求同时 401 时，**只允许一个请求发起刷新**，其余等待结果。后端在并发刷新时会返回：

```json
{ "code": 20109, "message": "刷新正在进行" }
```

前端应**静默重试**，不得弹错误提示。刷新失败（`20101`）才跳登录页。

### 会话被撤销

返回 `20101`。此时清空本地状态并跳登录页；不要尝试用旧 refresh 再刷一次。

## 4. 权限

- 权限码为三段式：`module:resource:action`，例如 `system:user:list`、`catalog:product:update`
- 登录后通过 `GET /api/v1/auth/menus` 获取：
  - `menus`：菜单树（含 `path`、`component`、`name`、子节点）
  - `permissions`：当前用户的权限码数组
- **UI 控制只是体验，不是安全边界**：后端每个接口独立校验，前端隐藏按钮不能替代授权
- `is_super` 只跳过 RBAC 判断，**不跳过数据范围**

## 5. 分页

### 普通分页

```
GET /api/v1/admin/users?page=1&page_size=20
```

```json
{ "list": [], "total": 0, "page": 1, "page_size": 20 }
```

- `page` 从 1 开始；`page_size` 默认 20，**超过 100 会被压到 100**
- 列表默认排序 `created_at desc`（除非接口另有说明）

### 深分页

部分列表（如商品）在 `page > 100` 时改用游标：

```
GET /api/v1/admin/products?cursor=<token>
```

- 游标不透明，**不要解析或拼接**
- 游标与筛选条件绑定：改变筛选后必须从头开始

## 6. 幂等

写接口支持可选请求头：

```
Idempotency-Key: <客户端生成的唯一串，≤256 字符>
```

- 有效期 24 小时，作用域为「操作者 + 操作」
- 相同 key、相同请求体 → 重放首次响应
- 相同 key、**不同**请求体 → `409 / 10006`
- 相同 key、首次请求仍在处理 → `409 / 10004`
- 部分计费写接口**强制**要求该头（见对应 OpenAPI）

## 7. 错误码（常见）

| code | HTTP | 含义 | 前端处理 |
|---|---|---|---|
| `10001` | 400 | 参数不合法 | 表单字段级提示 |
| `10002` | 500/503 | 服务内部错误 | 通用错误，可重试 |
| `10004` | 409 | 请求处理中 | 稍后用同一幂等键重试 |
| `10006` | 409 | 幂等键与请求体不一致 | 换新键重试 |
| `20101` | 401 | 未认证 / 会话失效 | 跳登录 |
| `20107` | 403 | 无权限 | 提示无权限，不跳转 |
| `20108` | 403 | 需要二次认证 | 弹重认证 |
| `20109` | 401 | 刷新正在进行 | **静默重试** |
| `10005` | 429 | 限流 | 按 `Retry-After` 退避 |
| `80201` | 404 | 接口不可用 | 不提示内部细节 |

完整映射见各域 OpenAPI 与 `docs/proposals/fe-0-review.md` §7。

## 8. 域索引

| 域 | 文件 | 主要能力 |
|---|---|---|
| 身份 | `identity.yaml` | 认证、用户、角色、菜单、权限、会话 |
| 店铺 | `shop.yaml` | 店铺、平台、凭据 |
| 同步 | `sync.yaml` | 同步任务、游标、死信、漂移 |
| 商品 | `catalog.yaml` | 类目、SPU/SKU、归并、导出 |
| 订单 | `order.yaml` | 列表、详情、统计、PII |
| 分享 | `share.yaml` / `share-access.yaml` / `share-inquiry.yaml` | 客户、报价、链接、匿名准入、询价 |
| 计费 | `billing.yaml` | 钱包、权益、对账、账单 |

## 9. 联调前置

后端需提供：

1. 一个可登录的测试账号，拥有 `system:user:list`
2. 种子数据：至少 3 个用户、2 个角色、完整菜单树
3. 本地跨域配置：允许前端开发服务器的 Origin（或前端走代理）

建议前端开发时用 Vite 代理转发 `/api`，避免 CORS 与 Cookie 域问题。
