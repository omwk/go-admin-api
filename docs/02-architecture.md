# 02 · 架构设计

- 版本：v1.0
- 关联：`docs/ADR.md`（决策理由）、`docs/03-data-model.md`（表结构）

---

## 1. 全景

```
                         ┌──────────────────────────────┐
  管理端 SPA  ──────────▶ │  Admin API  /api/v1/admin    │
  (现成 admin 模板)       │  JWT + RBAC + 数据权限        │
                         ├──────────────────────────────┤
  客户 H5     ──────────▶ │  Share API  /api/v1/share    │  免登录，token 鉴权
                         ├──────────────────────────────┤
  外部开发者  ──────────▶ │  Open Gateway /api/v1/open   │  HMAC 签名 + 限流 + 计量
                         └──────────────┬───────────────┘
                                        │
                        ┌───────────────▼────────────────┐
                        │        应用层（模块 service）     │
                        │ identity shop connector catalog │
                        │ order sync share gateway billing│
                        └───────────────┬────────────────┘
                                        │
        ┌───────────────┬───────────────┼───────────────┬──────────────┐
        ▼               ▼               ▼               ▼              ▼
   PostgreSQL 16     Redis 7       对象存储 S3      平台 API       外部 API
   (业务+原始报文)  (缓存/锁/队列)  (图片/导出/归档)  (淘宝/Shopee…)  (待接入)
                                        ▲
                                        │
                        ┌───────────────┴────────────────┐
                        │      Worker（独立进程）          │
                        │ asynq: 同步 / 刷新 / 计费 / 通知 │
                        └────────────────────────────────┘
```

**两个进程，一个代码库**：`cmd/api`（同步 HTTP 服务）、`cmd/worker`（异步任务）。二者共享 `internal/`，通过 Redis 队列通信。拆服务时按模块边界切即可。

---

## 2. 分层与依赖规则

```
handler (HTTP, 参数校验, 组装响应)
   ↓ 只能调用 ↓
service (业务编排, 事务边界, 领域规则)
   ↓ 只能调用 ↓
repo (数据访问, 只返回 model)      port (外部依赖接口: 平台、存储、通知)
   ↓                                ↓
        infrastructure 实现
```

**硬性规则（Codex 必须遵守）：**

1. `handler` 里不允许出现 SQL、GORM、`http.Client`。
2. `service` 里不允许出现 `gin.Context`、`*http.Request`；跨层传 `context.Context` + DTO。
3. 模块之间**只能**通过对方 `contract.go` 中导出的 interface 调用，禁止直接 import 别人的 `repo` 或 `model`。
4. 跨模块的「事后通知」走 Outbox 事件（`kernel/outbox`），不直接调用。
5. `repo` 返回领域 model，不返回 GORM 的 `*gorm.DB` 或 `Rows`。
6. 所有外部依赖（平台 API、对象存储、邮件）都必须定义 interface 放 `port/`，实现放 `internal/infra/`，便于测试替身。

---

## 3. 目录结构

```
commercehub/
├── cmd/
│   ├── api/main.go              # HTTP 服务
│   ├── worker/main.go           # 异步任务
│   └── cli/main.go              # 运维命令: migrate/replay/seed/rotate-key
├── internal/
│   ├── kernel/                  # 与业务无关的基础设施
│   │   ├── config/              # viper 加载 + 校验
│   │   ├── logger/              # slog + trace_id 注入
│   │   ├── errs/                # AppError + 错误码注册表
│   │   ├── db/                  # gorm 初始化、事务助手、分页
│   │   ├── cache/               # redis 客户端、锁、令牌桶
│   │   ├── httpx/               # gin 引擎、中间件、统一响应
│   │   ├── crypto/              # 信封加密、HMAC、随机 token
│   │   ├── idgen/               # 主键与公开 ID
│   │   ├── audit/               # 审计写入
│   │   ├── outbox/              # 事务内事件 + 投递器
│   │   ├── idem/                # 幂等键中间件与存储
│   │   └── obs/                 # metrics / tracing
│   ├── infra/                   # 外部实现
│   │   ├── storage/             # S3 / 本地
│   │   ├── notifier/            # 邮件 / webhook / 站内信
│   │   └── queue/               # asynq 封装
│   ├── modules/
│   │   ├── identity/
│   │   │   ├── contract.go      # 对外暴露的 interface + DTO
│   │   │   ├── handler/ service/ repo/ model/
│   │   ├── shop/                # 含 credential
│   │   ├── connector/           # 平台适配器
│   │   │   ├── types.go         # Platform interface, FetchRequest/Result
│   │   │   ├── registry.go
│   │   │   ├── errs.go          # 错误归一
│   │   │   └── platforms/
│   │   │       ├── shopee/
│   │   │       ├── taobao/
│   │   │       └── shopify/
│   │   ├── ingestion/           # raw 落地 + 映射引擎 + 漂移 + 重放
│   │   ├── catalog/
│   │   ├── order/
│   │   ├── sync/                # 调度、run、游标、死信
│   │   ├── share/
│   │   ├── gateway/
│   │   └── billing/
│   └── bootstrap/               # 依赖装配（wire 手写即可）
├── api/openapi/                 # 各里程碑导出
├── migrations/                  # golang-migrate
├── configs/
├── deploy/                      # Dockerfile, compose, k8s(后置)
├── docs/
└── test/                        # 集成测试、fixtures、mock 平台
```

> **禁止**出现 `internal/common`、`internal/utils` 这种垃圾桶目录。工具放 `kernel` 下按职责命名的子包。

---

## 4. 四条核心链路

### 4.1 同步链路（读上游 → 落库）

```
cron/手动
  → sync.Service.StartRun           创建 sync_run(running)，加 Redis 锁 sync:{shop}:{resource}
  → connector.Fetch(游标)            平台适配器：鉴权、限流、分页、错误归一
  → ingestion.SaveRaw                raw_payload 落库（sha256 去重）
  → ingestion.Apply(mappingVersion)  按映射解析 → canonical DTO
  │    ├─ 必填缺失 → quarantine_record
  │    └─ 结构漂移 → schema_drift_event
  → catalog/order.Upsert            幂等 upsert（比较 updated_at）
  → outbox.Emit(OrderSynced/ProductSynced)
  → sync_run(success, stats) + sync_cursor 前进
```

关键点：
- **raw 先落库再解析**。解析失败不影响原始数据保存，可随时用新映射重跑。
- 一个 run 处理 N 页后主动 re-enqueue，避免单任务过长（每任务 ≤ 60 秒）。
- 限流命中（429）不视为失败：记录 `Retry-After`，延时重排。

### 4.2 凭据链路

```
录入 → crypto.Seal(plaintext, aad=tenant:shop:kind) → credential_version(v1, cipher, key_version)
     → shop_credential(active, expires_at, refresh_at = expires_at - lead_time)
使用 → shop.Service.Credential(shopID) → 解密（内存中短暂存在）→ 传给 connector
      → 写 credential_audit(action=read)
刷新 → worker 扫描 refresh_at <= now + 5min → 分布式锁 cred:{id}
     → connector.Refresh() → 新版本 v(n+1) → 旧版本 retired
     → 失败：指数退避（1m/5m/30m/2h/6h），连续 3 次 → needs_reauth + 通知
```

### 4.3 分享链路

```
选品 + 报价单 → share_link(token_hash, mode, price_list_id, 过期/密码/次数)
  ├─ snapshot: 生成 share_snapshot(payload jsonb)，含商品快照 + 解析后价格
  └─ live:     不生成快照，访问时实时读商品，价格仍走报价单
客户访问 → /share/{token} → 校验状态/有效期/次数/密码 → 返回白名单字段
        → 写 share_visit（IP 哈希）
询价 → inquiry(new) → 站内信通知归属销售
```

### 4.4 网关链路

```
请求 → 路由匹配 → 应用鉴权(HMAC+时间戳+nonce) → 授权检查 → 限流(Redis 令牌桶)
    → 预估费用 → wallet.Freeze(原子条件更新) → 转发/内置执行
    → 成功: wallet.Commit | 失败: wallet.Release
    → 写 api_call_log（异步批量）→ usage_daily 聚合 → 对账
```

---

## 5. 技术选型

| 领域 | 选择 | 理由 | 备选与否决原因 |
|---|---|---|---|
| 语言 | Go 1.23 | 并发模型适合 IO 密集同步；部署简单 | — |
| HTTP | Gin | Codex 训练数据最多，生成质量稳定；中间件生态全 | Echo 类似但生态略小；go-zero 模板化重，易偏离设计 |
| ORM | GORM v2 | 开发效率高，迁移/关联成熟 | Ent 建模更好但 Codex 生成质量不稳；sqlc 太啰嗦 |
| 复杂查询 | 手写 SQL + `gorm.Raw` | 报表/多条件筛选 GORM 表达力不足 | — |
| 数据库 | PostgreSQL 16 | JSONB + GIN 索引 + 分区 + RLS + `ON CONFLICT` 幂等 | MySQL 8 JSON 功能弱，分区限制多 |
| 缓存/锁/限流 | Redis 7 | 分布式锁、令牌桶（Lua）、幂等键 | — |
| 队列 | asynq | 基于 Redis，无需额外中间件；支持 cron、重试、死信 | Kafka 过重；River 绑 PG 但生态较新 |
| 迁移 | golang-migrate | 简单、SQL 原生、可 down | Atlas 功能强但引入声明式心智 |
| 配置 | Viper | 环境变量/文件/默认值优先级清晰 | koanf 更轻但 Codex 不熟 |
| 校验 | go-playground/validator | Gin 默认集成 | — |
| 日志 | log/slog + JSON handler | 标准库，无额外依赖 | zap 更快但 slog 够用 |
| 指标 | Prometheus client | 事实标准 | — |
| 链路 | OpenTelemetry（可后置） | 与 trace_id 对齐 | — |
| 表达式 | expr-lang/expr | 类型安全、可静态检查、性能好，用于映射转换 | govaluate 已停更；CEL 依赖重 |
| ID | 内部 `bigint identity`；对外 `ULID`/随机 token | 内部 join 快，对外不可枚举 | 全 UUID 影响索引局部性 |
| API 文档 | 手写 OpenAPI 3 + CI diff | 契约先于实现，防止 Codex 自由发挥 | swag 注解易漂移 |
| 测试 | testify + testcontainers(可选) | 表驱动 + 断言清晰 | — |
| 部署 | Docker Compose → 单机/云主机 | 单体不需要 K8s | K8s 后置 |

---

## 6. 模块依赖图（谁可以调谁）

```
identity ◀── 所有模块（取当前用户/数据权限）
shop     ◀── connector, sync, catalog, order
connector◀── sync（只通过 registry 取 Platform 实现）
ingestion◀── sync
catalog  ◀── share（读商品）, ingestion（写）
order    ◀── （无下游）
share    ◀── catalog, shop
gateway  ◀── billing（扣费）, identity（内部路由鉴权）
billing  ◀── （无下游）
```

**禁止反向依赖**：`order` 不能 import `share`；`connector` 不能 import `catalog`。需要反向通知时用 Outbox 事件。

---

## 7. 横向能力

### 7.1 认证与授权
- 管理端：JWT（access 15min / refresh 7d），refresh 存哈希；登出/强制下线进 Redis 黑名单（TTL = 剩余有效期）。
- 权限：`permission_code` 中间件 + 数据权限注入（`DataScope` 解析成 SQL 条件）。
- 分享端：URL token（只存哈希），不签发 JWT。
- 网关端：HMAC 签名，不用 JWT。

### 7.2 多租户
- 所有业务表带 `tenant_id`，默认 1。
- GORM 全局 Scope 自动注入 `tenant_id`；后台任务从任务载荷取。
- 唯一索引一律以 `tenant_id` 开头。
- 未来切 RLS 时，只需加策略 + 设置 `app.tenant_id`。

### 7.3 幂等
- 写接口支持 `Idempotency-Key` 头 → `idempotency_key` 表（scope + key 唯一），24h。
- 同步写入用业务唯一键 + `ON CONFLICT DO UPDATE`。
- 队列消费用 `job_id` + 业务键去重。

### 7.4 可观测
- 日志字段固定：`ts level msg trace_id tenant_id user_id module action duration_ms error_code`。
- 指标：HTTP（QPS/延迟/错误率）、同步（run 数、抓取量、隔离量）、凭据（临期数、刷新失败数）、网关（调用量、429、扣费失败）、队列（积压）。
- 告警：凭据 `needs_reauth`、漂移事件未处理 > 24h、同步连续失败 3 次、对账差异 > 0。

### 7.5 安全
- 凭据与 PII：AES-256-GCM 信封加密，主密钥来自 `CRED_MASTER_KEY`（32 字节 base64），支持 `key_version` 轮换。
- 密码：bcrypt cost 12。
- 签名比较用 `hmac.Equal`，防时序攻击。
- 分享 token / app secret 只存哈希（sha256 + salt 或 bcrypt 视用途）。
- 全站强制 HTTPS（反代层）；CORS 白名单；上传文件类型与大小白名单。

---

## 8. 部署

```
              ┌────────────┐
  Internet ──▶│  Nginx/    │──▶ api:8080 (x2)
              │  Caddy     │──▶ worker (x1, 可横向)
              └────────────┘
                     │
        ┌────────────┼────────────┐
        ▼            ▼            ▼
   PostgreSQL     Redis        MinIO/S3
```

- 单机：`docker compose` 起 pg/redis/minio + api + worker。
- 扩容：api 无状态直接加副本；worker 按资源类型分队列（sync / refresh / billing / notify）分别扩。
- 迁移：部署前跑 `cmd/cli migrate up`，用 advisory lock 防并发。
- 备份：PG 每日全量 + WAL 归档；对象存储版本化。

---

## 9. 扩展点（后续加功能怎么加）

| 要加的东西 | 怎么做 | 不允许 |
|---|---|---|
| 新平台 | 实现 `connector.Platform` + 注册 + 配 mapping profile + 插 platform 记录 | 改 sync/ingestion 代码 |
| 新同步资源（如售后单） | 加 `resource_type` 枚举 + mapping + 目标表 + 落库 handler | 复制一套同步框架 |
| 新网关上游 | 后台加 `api_route` 记录 | 改网关代码 |
| 新计费模式 | 加 `pricing_plan.mode` 枚举 + billing 策略实现 | 改钱包表结构 |
| 新对外端（如小程序） | 复用 share API，换展示层 | 复制业务逻辑 |
| 新租户 | 插 tenant 记录 + 初始化角色菜单 | 分库分表（暂不需要） |

---

## 10. 关键取舍记录

1. **为什么原始报文一定要落库**：它是唯一能在上游改字段后「重新解释历史」的证据。没有它，重放无从谈起。成本是存储，用分区 + 归档解决。
2. **为什么映射放数据库而不是代码**：平台字段变更频率远高于发版频率；运营/实施能改映射，比等开发快得多。代价是需要一套表达式 DSL 和校验。
3. **为什么分享用快照而不是实时**：对外承诺不能因为上游改价、下架、改字段而变。快照是把「对外契约」冻结下来的唯一手段。
4. **为什么网关要预扣费**：后付费在开放场景下必然被刷爆；预扣 + 日终对账是唯一能同时保证不超支和账目准确的做法。
5. **为什么不上微服务**：当前规模（单租户、日均 5 万订单）单机 PG 完全够；微服务会把「一个事务」拆成「分布式事务」，收益为负。模块边界已经画好，真到瓶颈再拆。
