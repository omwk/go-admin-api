# 05 · 平台接入与同步规格

- 关联：`docs/03-data-model.md` §4、§7；`docs/ADR.md` ADR-002 / ADR-004
- 适用里程碑：M3（框架 + 首个平台）、M4（商品）、M5（订单）、M7（刷新）

---

## 1. 设计原则

1. **适配器只负责「取」，不负责「解释」**。取回来的是原始 JSON，字段含义交给映射配置。
2. **先落库，再解析**。任何解析失败都不能导致原始数据丢失。
3. **同一份数据可以被反复解释**。映射改了就重放历史，不用重新拉。
4. **平台差异不进入业务代码**。业务层只认规范模型 + `ext`。

---

## 2. Connector 接口

```go
package connector

type ResourceType string

const (
    ResourceProduct   ResourceType = "product"
    ResourceOrder     ResourceType = "order"
    ResourceInventory ResourceType = "inventory"
    ResourceCategory  ResourceType = "category"
)

type Capabilities struct {
    Resources            []ResourceType
    SupportsWebhook      bool
    SupportsTokenRefresh bool
    MaxPageSize          int           // 0 = 不限制
    MinInterval          time.Duration // 同一店铺两次请求的最小间隔
    IncrementalModes     []string      // "timestamp" / "cursor" / "id_watermark"
}

type Credential struct {
    Kind      string            // oauth2 / api_key / cookie / session
    Values    map[string]string // 明文，仅在内存中存在，禁止打印
    ExpiresAt time.Time
}

type FetchRequest struct {
    Resource   ResourceType
    Cursor     string    // 空 = 从头开始
    Since      time.Time // 增量起点（零值 = 全量）
    Until      time.Time
    PageSize   int
    Params     map[string]any // 平台特有参数（如 shop_id、时间字段名）
}

type RawItem struct {
    ExternalID string
    Payload    json.RawMessage
}

type FetchResult struct {
    Items      []RawItem
    NextCursor string
    HasMore    bool
    FetchedAt  time.Time
}

// Platform 每个平台实现一次，在 registry 中注册。
type Platform interface {
    Code() string
    Capabilities() Capabilities
    // Check 用一个最轻量的接口验证凭据是否可用。
    Check(ctx context.Context, cred Credential) error
    // Refresh 返回刷新后的凭据；不支持刷新返回 ErrUnsupported。
    Refresh(ctx context.Context, cred Credential) (Credential, error)
    // Fetch 拉取一页数据。实现内部必须处理鉴权头、签名、分页参数。
    Fetch(ctx context.Context, req FetchRequest, cred Credential) (*FetchResult, error)
}
```

**注册方式**：`internal/modules/connector/registry.go` 里 `Register(platform Platform)`，在 `bootstrap` 中按配置启用。新增平台**不允许**修改 `sync` / `ingestion` 模块。

---

## 3. 错误归一

平台错误必须映射成统一类型，**禁止**把 `*url.Error` 或上游 JSON 直接抛给上层。

```go
type ErrKind string

const (
    ErrKindAuthExpired ErrKind = "auth_expired" // 可刷新
    ErrKindAuthInvalid ErrKind = "auth_invalid" // 需人工重新授权
    ErrKindRateLimited ErrKind = "rate_limited"
    ErrKindNotFound    ErrKind = "not_found"
    ErrKindInvalidParam ErrKind = "invalid_param"
    ErrKindUpstream    ErrKind = "upstream_error"
    ErrKindNetwork     ErrKind = "network"
    ErrKindUnsupported ErrKind = "unsupported"
)

type PlatformError struct {
    Platform   string
    Op         string
    Kind       ErrKind
    HTTPStatus int
    Code       string        // 平台错误码，仅日志用
    Message    string        // 脱敏后的描述
    Retryable  bool
    RetryAfter time.Duration
}
func (e *PlatformError) Error() string
func (e *PlatformError) Unwrap() error
```

### 3.1 处理策略表

| Kind | 同步引擎行为 | 是否进死信 |
|---|---|---|
| `auth_expired` | 触发一次同步刷新 → 重试；仍失败则 `needs_reauth` + 暂停任务 | 否 |
| `auth_invalid` | 直接暂停任务 + 通知 | 否（进凭据告警） |
| `rate_limited` | 按 `RetryAfter`（缺省 60s）延时重排，最多 5 次 | 超过 5 次 |
| `network` / `upstream` | 指数退避重试 3 次（1s/4s/16s + jitter） | 是 |
| `invalid_param` | 不重试 | 是 |
| `not_found` | 标记该条为 `vanished` / 跳过 | 否 |
| `unsupported` | 记录并跳过该资源 | 否 |

### 3.2 各平台映射要点

| 平台 | 过期判定 | 限流信号 | 备注 |
|---|---|---|---|
| Shopee | HTTP 401 + `error=error_auth` | HTTP 429 | 签名参数 `sign` 需按文档顺序拼接 |
| 淘宝 TOP | `error_response.code` 为 26/27 系列 | `sub_code` 含 `limit` | session key 过期需用户重新授权 |
| Shopify | HTTP 401 / 403 | HTTP 429 + `Retry-After` | 静态 token，不刷新 |

> 上表是实现时的初始判断，**必须以平台当期文档为准**，并在适配器单测的 fixture 中固化。

---

## 4. 分页与增量

### 4.1 三种模式

| 模式 | 适用 | 游标存什么 | 注意 |
|---|---|---|---|
| `timestamp` | 订单（按更新时间） | 上次成功的最新 `update_time` | 必须回看窗口（默认 7 天）防漏单 |
| `cursor` / `page_token` | 商品、Shopee 列表 | 平台返回的 `next_cursor` | 游标可能过期，需能从头重建 |
| `id_watermark` | 自增 ID 类 | 最大外部 ID | 只适合严格递增 |

### 4.2 单次 run 的边界

- 单次 run 最多处理 `max_pages_per_run`（默认 20）页，每页 ≤ 500 条。
- 达到上限后：保存游标 → 立即 re-enqueue 一个续跑任务 → 当前 run 标记 `partial`。
- 单页处理超过 60 秒视为异常，记录并继续下一页。
- 全量刷新单独用一个 cron（默认每日 03:00），与增量任务互斥（同一把锁）。

### 4.3 游标推进规则

**只有当前页全部成功落库（含隔离）才推进游标。** 部分失败时：
- 可重试错误 → 不推进，重排队。
- 不可重试错误 → 该条进死信，其余推进（避免一条坏数据卡死整条链路）。

---

## 5. 限流与并发

- 每店铺一把 Redis 锁：`lock:sync:{shop_id}:{resource}`，TTL 30 分钟，续期间隔 10 秒。
- 平台级令牌桶：`rl:platform:{platform}:{shop_id}`，速率 = `min(平台配额, 配置值)`，用 Lua 原子取。
- 适配器声明 `MinInterval`，同步引擎在两次 `Fetch` 之间至少等待该间隔。
- 同一平台的所有店铺共享一个「平台总配额」桶（可选，配置开启）。

---

## 6. 映射规格（Mapping Spec）

### 6.1 结构

```json
{
  "schema_version": 1,
  "resource": "order",
  "key_field": "external_order_id",
  "fields": [
    {
      "target": "external_order_id",
      "source": "$.order_sn",
      "required": true
    },
    {
      "target": "total_amount_cents",
      "source": "$.total_amount",
      "required": true,
      "transform": "money_cents(value)"
    },
    {
      "target": "status",
      "source": "$.order_status",
      "transform": "map_status(value, 'shopee_order_status')"
    },
    {
      "target": "placed_at",
      "source": "$.create_time",
      "transform": "ts_unix(value)"
    },
    {
      "target": "ext.logistics_channel",
      "source": "$.logistics_channel"
    }
  ],
  "collections": [
    {
      "target": "items",
      "source": "$.item_list",
      "key_field": "external_item_id",
      "fields": [
        { "target": "external_item_id", "source": "$.item_id", "required": true },
        { "target": "title", "source": "$.item_name" },
        { "target": "qty", "source": "$.model_quantity_purchased", "transform": "int(value)" },
        { "target": "price_cents", "source": "$.model_discounted_price", "transform": "money_cents(value)" }
      ]
    }
  ]
}
```

### 6.2 规则

| 项 | 规定 |
|---|---|
| `source` | JSONPath，根为 `$`；数组元素内用 `$.` 相对路径（引擎注入当前元素为根） |
| `target` | 规范模型字段名；`ext.` 前缀表示写入扩展 JSON |
| `required` | `true` 时缺失 → 该条记录进隔离区，**不阻塞批次** |
| `transform` | expr 表达式，变量 `value` 为取到的原值；可空（原样赋值） |
| 类型转换 | 必须显式，禁止依赖隐式转换；转换失败 → `quarantine`（`type_mismatch`） |
| `key_field` | 规范模型的业务唯一键，用于 upsert |
| `collections` | 一对多子表（订单明细、SKU 列表），逐元素映射后整体替换 |
| 未声明的字段 | 不进入规范模型；但 raw 已保存，随时可加映射后重放 |

### 6.3 内置函数

| 函数 | 说明 | 示例 |
|---|---|---|
| `trim(v)` / `upper(v)` / `lower(v)` | 字符串处理 | `trim(value)` |
| `int(v)` / `decimal(v)` | 转数字，失败返回 error | `int(value)` |
| `money_cents(v)` | 金额转「分」，字符串/浮点都接受，四舍五入到分 | `money_cents(value)` |
| `ts_unix(v)` / `ts_ms(v)` / `ts_str(v, layout)` | 时间转 UTC | `ts_str(value, "2006-01-02 15:04:05")` |
| `map_status(v, 'table')` | 按状态映射表转换，未命中返回原值并记 warn | `map_status(value,'shopee_order_status')` |
| `default(v, x)` | v 为空时取 x | `default(value, "unknown")` |
| `coalesce(a,b,...)` | 取第一个非空 | `coalesce(value, $.fallback)` |
| `concat(a,b,...)` / `split(v, sep)` / `join(arr, sep)` | 字符串 | |
| `substr(v, start, len)` / `replace(v, old, new)` | | |
| `regex_extract(v, pattern, group)` | 正则提取 | |
| `to_bool(v)` | `"1"/"true"/"Y"` → true | |
| `json_raw(v)` | 原样序列化进 `ext` | |

### 6.4 状态映射表

存 `sys_setting`，`group_key='status_map'`：

```json
{
  "shopee_order_status": {
    "UNPAID": "pending_payment",
    "READY_TO_SHIP": "pending_shipment",
    "SHIPPED": "shipped",
    "COMPLETED": "completed",
    "CANCELLED": "closed",
    "IN_CANCEL": "refunding",
    "TO_RETURN": "refunding"
  }
}
```

- 未命中的状态**不丢弃**：规范状态置 `unknown`，原值进 `ext.platform_status_raw`，并产生一条 warn 日志 + 指标。
- 状态映射表的修改不影响历史数据（历史已落库），需要时可重放。

### 6.5 发布与校验流程

```
draft 保存 → 静态校验（JSONPath 语法 / expr 编译 / 目标字段存在）
          → dry-run（对最近 20 条 raw 应用，展示映射结果 diff）
          → 人工确认 → 发布（status=published, 生成 checksum）
          → profile.active_version_id 指向新版本
```

**已发布版本不可修改**：service 层直接拒绝更新，只能新建 draft。

---

## 7. 结构漂移检测

### 7.1 指纹算法

```
walk(json, path="$"):
  对每个叶子: 记录 "path|type"    // type ∈ string|number|bool|null|array|object
  数组: 取所有元素的并集，元素路径统一为 path[] （避免索引爆炸）
fingerprint = sha256(sorted(entries))
```

### 7.2 对比与产出

每次成功解析后（每批一次，不做逐条）：

```
added   = current - baseline      → field_added,   severity=info
removed = baseline - current      → field_removed, severity = 若该 path 被任一 mapping_rule 的 source 引用且 required → critical，否则 warning
changed = 同 path 类型不同         → type_changed,  severity=warning（若被 required 引用则 critical）
```

- 同一 `(platform, resource, kind, path)` 在 24 小时内只记一条，避免刷屏。
- `critical` 事件：立即发通知 + 在管理端首页红点。
- 处理动作：`acked`（知道了，暂不处理）/ `resolved`（已改映射）/ `ignored`（该字段不重要，同时把该 path 加入白名单）。

### 7.3 基线更新

- 基线**不自动更新**。人处理完漂移事件后，显式点「设为新基线」→ 旧基线 `superseded`，新基线 `active`。
- 首次接入某平台时，前 3 次成功解析不产生漂移事件（避免初始化噪音），第 3 次后自动建立基线。

### 7.4 与隔离区的关系

| 情况 | 结果 |
|---|---|
| 可选字段缺失 | 规范字段为空，不隔离，产生 drift（warning） |
| 必填字段缺失 | 该条进 `quarantine_record`（`missing_required`），drift（critical） |
| 类型不符但可转换 | 转换成功则不隔离，记一条 warn 指标 |
| 类型不符且无法转换 | 隔离（`type_mismatch`） |
| 未知字段 | 忽略（raw 已存），drift（info） |

---

## 8. 隔离区与重放

### 8.1 隔离区处理

- 管理端列表：按平台/店铺/资源/原因筛选，展示缺失字段详情 + 原始报文链接。
- 操作：`重放`（修复映射后单条/批量）、`丢弃`（标记不再处理）、`导出`。
- 重放走与正常同步相同的 `Apply + Upsert` 路径，保证行为一致。

### 8.2 历史重放（REQ-SYNC-011）

**触发场景**：映射修正、状态映射表调整、规范模型新增字段。

**前置条件**：新映射版本已发布；`replay_run` 指定范围。

**流程**：

```
1. 创建 replay_run(resource, mapping_version_id, 范围)
2. worker 分批（每批 200 条 raw，按 raw_id 升序）
3. 每条：Apply(新映射) → Upsert 规范层 → 累加 stats
4. 失败进 dead_letter，不中断批次
5. 完成后：抽样 20 条人工核对 + 输出「变更条数/隔离条数/失败条数」
```

**范围选择**：
- 按时间：`fetched_at between ? and ?`
- 按 ID：`id between ? and ?`
- 仅隔离区：`quarantine_record.status='pending'`

**安全阀**：
- 单次重放上限 50 万条，超过必须分批人工触发。
- 重放不删除规范层数据；只 upsert。若规范模型删了字段，旧值保留（避免误删）。
- 重放期间同步任务可继续跑，但同一 `(shop, resource)` 的 replay 与 sync 互斥（同一把锁）。

---

## 9. 同步监控指标

| 指标 | 类型 | 说明 |
|---|---|---|
| `sync_run_total{shop,resource,status}` | counter | run 结果分布 |
| `sync_items_total{shop,resource,result}` | counter | created/updated/skipped/quarantined/failed |
| `sync_duration_seconds{shop,resource}` | histogram | run 耗时 |
| `sync_upstream_errors_total{platform,kind}` | counter | 按错误类型 |
| `sync_cursor_lag_seconds{shop,resource}` | gauge | 游标时间与当前时间差（增量延迟） |
| `drift_events_open{platform,resource}` | gauge | 未处理漂移数 |
| `quarantine_pending{shop}` | gauge | 待处理隔离数 |

告警阈值（写进 `configs/alerts.yml`）：
- `sync_cursor_lag_seconds > 3600` 持续 10 分钟
- 连续 3 次 run `failed`
- `drift_events_open` 新增 critical
- `quarantine_pending > 1000`

---

## 10. 测试要求

每个平台适配器必须提供：

1. `testdata/*.json` —— 脱敏后的真实响应（至少：正常页、空页、错误响应）。
2. `httptest.Server` 模拟平台，覆盖：
   - 正常分页两页
   - 401 过期 → 触发刷新 → 重试成功
   - 429 → 返回 `Retry-After` → 验证退避
   - 必填字段缺失 → 进隔离区
   - 多余字段 → 进 `ext`，不报错
3. 映射引擎单测：每个内置函数至少一个用例；错误路径（除零、空值、类型错）必须有断言。
4. 幂等测试：同一批数据跑两次，规范层条数不变、`updated_at` 不变。

---

## 11. 接入新平台 Checklist

- [ ] `platform` 表插入记录（code、auth_type、capabilities、config_schema）
- [ ] 实现 `connector.Platform`，注册到 registry
- [ ] 补齐错误映射（`errs.go` 中的 `MapXxxError`）
- [ ] 写 `testdata` + httptest 单测
- [ ] 配 `mapping_profile` + 首个 `mapping_version`（先用 dry-run 验证）
- [ ] 建 `sync_job`（增量 + 全量两条 cron）
- [ ] 在 `docs/05-platform-sync.md` §3.2 补一行能力矩阵
- [ ] 跑通一次真实同步，检查 raw / canonical / drift 三条链路
- [ ] 配告警
