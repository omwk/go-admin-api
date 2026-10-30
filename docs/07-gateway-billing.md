# 07 · 开放网关与计费规格

- 关联：`docs/03-data-model.md` §9、§10；`docs/ADR.md` ADR-007
- 适用里程碑：M8（网关）、M9（计费）

---

## 1. 定位

两条通道：

| 通道 | 路径 | 使用者 | 鉴权 | 计费 |
|---|---|---|---|---|
| 内部 | `/api/v1/admin/**` | 自己人 | JWT | 不计费 |
| 开放 | `/api/v1/open/**` | 外部开发者（白名单） | HMAC 签名 | 按配置 |

网关同时支持两种上游：
- `http`：把请求代理到第三方 API（你从网上找的实用 API）。
- `handler`：调用本系统内部能力（如「查订单状态」），注册在代码里的 handler key。

---

## 2. 路由模型

```json
{
  "code": "weather.now",
  "method": "GET",
  "path": "/api/v1/open/weather/now",
  "upstream_type": "http",
  "upstream_url": "https://api.example.com/v3/weather?city={city}",
  "auth_mode": "hmac",
  "visibility": "public",
  "timeout_ms": 5000,
  "retry_count": 1,
  "cache_ttl_seconds": 60,
  "rate_limit_qps": 10,
  "rate_limit_burst": 20,
  "billing_mode": "per_call",
  "price_cents": 5
}
```

**路径匹配**：精确匹配优先，其次前缀匹配（`/api/v1/open/weather/*`）。不支持正则，避免性能与安全陷阱。

**参数透传**：
- `upstream_url` 中 `{name}` 从 path/query 取值并做 URL 编码。
- 请求体原样转发（有大小上限，默认 1MB）。
- 只透传白名单请求头（`Content-Type`、`Accept`、`X-Request-Id`），**不透传** `Authorization`、`Cookie`。
- 上游密钥由网关注入（存在 `api_route` 的加密配置里），不暴露给调用方。

---

## 3. 鉴权：HMAC 签名

### 3.1 请求头

```
X-App-Id:    <app_id>
X-Timestamp: <unix 秒>
X-Nonce:     <32 位随机字符串>
X-Signature: <base64(hmac-sha256(secret, string_to_sign))>
```

### 3.2 待签串

```
string_to_sign =
  METHOD + "\n" +
  PATH + "\n" +
  canonical_query + "\n" +
  X-Timestamp + "\n" +
  X-Nonce + "\n" +
  hex(sha256(body))
```

- `PATH`：不含 query 的请求路径，原样（不 decode）。
- `canonical_query`：query 参数按 key 字典序排列，`k=v` 用 `&` 连接，值做 URL 编码（`url.QueryEscape`），空值保留 `k=`。
- `body`：空请求体时为 `hex(sha256(""))`。

### 3.3 校验步骤

```
1. app_id 存在且 status=active                → 否则 80101
2. |now - timestamp| <= 300s                   → 否则 80103
3. nonce 在 Redis SETNX app:{app_id}:nonce:{nonce} TTL=600 → 失败则 80103
4. 取该 app 所有 active secret，逐个验签（常量时间比较）→ 全失败则 80102
5. IP 白名单（若配置）                          → 否则 80105
6. 路由授权 api_client_route(status=active, 未过期) → 否则 80104
```

> 第 3 步必须在第 4 步**之前**做，防止重放攻击消耗验签 CPU。nonce 先占位，验签失败也保留（宁可误杀不可放过）。

### 3.4 为什么不用 JWT

外部调用方可能是脚本、Postman、第三方服务，签名方案无状态、易实现、不需要刷新 token。JWT 更适合浏览器会话。

---

## 4. 限流

三层，任一命中即 429：

| 层 | Key | 默认 |
|---|---|---|
| 全局 | `rl:global` | 配置值（如 5000 qps） |
| 路由 | `rl:route:{route_id}` | `api_route.rate_limit_qps` |
| 应用×路由 | `rl:client:{client_id}:route:{route_id}` | 取 min(路由配置, 应用配额) |

- 算法：Redis Lua 令牌桶（`tokens` + `last_refill`，原子取）。
- 响应头：`X-RateLimit-Limit`、`X-RateLimit-Remaining`、`Retry-After`。
- 429 仍然写调用日志（`billed=false`），用于分析滥用。

---

## 5. 计费流程

### 5.1 状态机

```
        ┌──────────┐  成功   ┌──────────┐
请求 →  │ 预估费用  │ ──────▶ │  freeze  │ ──▶ 调用上游 ──┬─ 2xx ──▶ commit（冻结转扣减）
        └──────────┘         └──────────┘               │
                                     │                   └─ 5xx/超时 ──▶ release（解冻）
                                     └─ 余额不足 ──▶ 402 / 90102（不发起调用）
```

### 5.2 三种计费模式

| mode | 扣减方式 | 说明 |
|---|---|---|
| `free` | 不计费 | 内部或引流接口 |
| `per_call` | 每次调用扣 `price_cents`（最小 1 分） | 余额不足直接拒绝 |
| `package` | 每次消耗 `units_per_call` 个套餐单元 | 优先扣套餐，套餐用尽回落到 `per_call` 或拒绝 |

> **单价不足 1 分怎么办**：不要引入小数。用套餐包表达（例如 1000 次 / 100 分，等效 0.1 分/次）。

### 5.3 冻结（关键 SQL）

```sql
-- 原子冻结：余额减冻结 >= 本次预估
UPDATE wallet
SET frozen_cents = frozen_cents + $1,
    version = version + 1,
    updated_at = now()
WHERE id = $2
  AND status = 'active'
  AND balance_cents - frozen_cents >= $1
RETURNING balance_cents, frozen_cents;
```

- 影响行数 0 → 余额不足（`90102`）。
- 冻结后写 `wallet_txn(type='freeze', idempotency_key=trace_id)`。
- 若同一 `trace_id` 已存在 freeze 记录 → 视为重试，直接取已有冻结额，不重复冻结。

### 5.4 提交 / 释放

```sql
-- commit
UPDATE wallet
SET balance_cents = balance_cents - $1,
    frozen_cents  = frozen_cents - $1,
    version = version + 1
WHERE id = $2 AND frozen_cents >= $1;

-- release
UPDATE wallet
SET frozen_cents = frozen_cents - $1,
    version = version + 1
WHERE id = $2 AND frozen_cents >= $1;
```

- 都要写 `wallet_txn`，`idempotency_key = trace_id + ":" + type`。
- 释放失败（冻结被别的流程吃掉）→ 记 `90401` 对账异常，人工介入，**不允许静默吞掉**。

### 5.5 套餐扣减

```sql
UPDATE subscription
SET remaining_units = remaining_units - $1
WHERE id = $2 AND status = 'active' AND ends_at > now() AND remaining_units >= $1
RETURNING remaining_units;
```

- 影响行数 0 → 套餐不可用，按 `per_call` 回退或拒绝。
- 套餐扣减不写钱包流水，但写 `usage_daily.units`。

---

## 6. 计量与对账

### 6.1 日志写入

- 请求结束后异步写入 `api_call_log`（批量，每 500 条或 1 秒 flush）。
- 进程退出前必须 flush；失败落本地文件兜底（`logs/call_log_fallback.jsonl`），启动时补写。

### 6.2 日聚合

每日 00:30 跑 `usage_daily` 聚合：

```sql
INSERT INTO usage_daily (tenant_id, client_id, route_id, stat_date, calls, error_calls, units, amount_cents)
SELECT tenant_id, client_id, route_id, called_at::date,
       count(*),
       count(*) FILTER (WHERE status_code >= 400),
       sum(cost_units),
       sum(cost_cents)
FROM api_call_log
WHERE called_at >= $1 AND called_at < $2
GROUP BY 1,2,3,4
ON CONFLICT (tenant_id, client_id, route_id, stat_date)
DO UPDATE SET calls = EXCLUDED.calls, error_calls = EXCLUDED.error_calls,
              units = EXCLUDED.units, amount_cents = EXCLUDED.amount_cents, updated_at = now();
```

### 6.3 日终对账（02:00）

```
对每个 client：
  a = sum(usage_daily.amount_cents) where stat_date = 昨天
  b = sum(wallet_txn.amount_cents) where type='commit' and created_at 在昨天
  若 a != b → 写 billing_reconcile_diff 记录 + 告警 90401
```

差异常见原因：
- 调用成功但 commit 丢失（进程崩溃）→ 需补偿任务扫「有 freeze 无 commit」的记录。
- 上游超时判失败但实际成功 → 业务上按网关判定为准，差异记录留档。

---

## 7. 应用与密钥管理

- `app_id`：ULID，公开。
- `secret`：32 字节随机，base64；**只在创建时返回一次**，库里存 sha256。
- 一个应用最多 2 个 `active` secret（平滑轮换）：新建 → 调用方切换 → 旧 secret 设过期 → 自动清理。
- `last_used_at` 每次调用异步更新（不阻塞请求）。
- 停用应用：立即拒绝（缓存 30 秒内生效，用 Redis 版本号触发失效）。

---

## 8. 上游保护

| 项 | 默认 | 说明 |
|---|---|---|
| 超时 | 5s（路由可配） | 连接 2s / 读 3s |
| 重试 | 1 次，仅 GET/HEAD | POST 不自动重试（除非幂等头存在） |
| 熔断 | 连续 20 次失败 → 打开 30s | 半开试探 3 次 |
| 响应体上限 | 2MB | 超过截断并记 warn |
| 请求体上限 | 1MB | 超过 413 |
| 连接复用 | 每上游 host 一个 Transport | 禁止每次 new client |
| DNS 缓存 | 5 分钟 | 防止 DNS 抖动 |

**SSRF 防护**：`upstream_url` 必须是 https，禁止内网 IP 段（10/8、172.16/12、192.168/16、127/8、169.254/16），解析后的 IP 也要校验。仅允许在配置中显式加白的内网地址。

---

## 9. 接口清单

### 9.1 管理端

| 方法 | 路径 | 说明 |
|---|---|---|
| GET/POST | `/api/v1/admin/api-routes` | 路由列表/创建 |
| PUT/DELETE | `/api/v1/admin/api-routes/{id}` | 更新/停用 |
| POST | `/api/v1/admin/api-routes/{id}/test` | 测试调用（不扣费） |
| GET/POST | `/api/v1/admin/api-clients` | 应用列表/创建（创建返回 secret，仅一次） |
| POST | `/api/v1/admin/api-clients/{id}/secrets` | 新建密钥 |
| DELETE | `/api/v1/admin/api-clients/{id}/secrets/{sid}` | 吊销密钥 |
| POST | `/api/v1/admin/api-clients/{id}/routes` | 授权路由 |
| GET | `/api/v1/admin/api-call-logs` | 调用日志（按应用/路由/时间） |
| GET | `/api/v1/admin/wallets` | 钱包列表 |
| POST | `/api/v1/admin/wallets/{id}/recharge` | 人工充值 |
| GET | `/api/v1/admin/wallet-txns` | 流水 |
| GET/POST | `/api/v1/admin/pricing-plans` | 套餐 |
| POST | `/api/v1/admin/recharge-codes` | 生成兑换码（批量） |
| GET | `/api/v1/admin/billing-statements` | 账单 |
| GET | `/api/v1/admin/reconcile-diffs` | 对账差异 |

### 9.2 开放端

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/open/usage` | 查询自己的用量（免计费） |
| GET | `/api/v1/open/balance` | 查询余额 |
| GET | `/api/v1/open/routes` | 查询已授权路由列表 |
| * | `/api/v1/open/{route_path}` | 动态路由 |

> `usage` / `balance` / `routes` 是系统内置路由，不走动态匹配，且不计费。

---

## 10. 错误与降级

| 场景 | 返回 | 处理 |
|---|---|---|
| 余额不足 | 402 `90102` | 不发起上游调用 |
| 应用停用 | 401 `80101` | |
| 签名错 | 401 `80102` | 不暴露具体哪个字段错 |
| 重放 | 401 `80103` | |
| 未授权路由 | 403 `80104` | |
| 限流 | 429 `10005` + `Retry-After` | |
| 上游 5xx | 502 `80301` | 已释放冻结 |
| 上游超时 | 504 `80302` | 已释放冻结 |
| 计费系统不可用 | 503 `90101` | **fail closed**，不免费放行 |

**fail closed 原则**：计费组件故障时拒绝服务，不能因为「扣不了钱」就免费放行——否则一旦被发现有漏洞会被刷爆。

---

## 11. 测试要求

1. 签名：正确签名通过；时间戳偏移 301s 拒绝；同 nonce 第二次拒绝；错误 secret 拒绝。
2. 限流：并发 100 请求、QPS=10，通过的 ≤ burst + qps×时长。
3. 计费：
   - 余额 100 分，单价 5 分，并发 30 次 → 成功 20 次，其余 402，最终余额 0。
   - 上游 500 → 冻结全部释放，余额不变。
   - 同一 trace_id 重复提交 → 只扣一次。
4. 对账：人为少写一条 commit → 对账任务能发现差异。
5. 熔断：上游连续失败 20 次后，后续请求快速失败，30s 后半开恢复。
6. SSRF：`upstream_url` 指向 `http://127.0.0.1` 或 `http://10.0.0.1` 必须被拒绝。
