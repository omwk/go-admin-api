# 04 · 工程约定

- 状态：强制执行。`AGENTS.md` 引用本文件；违反即为缺陷。
- 关联：`docs/03-data-model.md`、`docs/ADR.md`

---

## 1. 命名

| 对象 | 规则 | 示例 |
|---|---|---|
| Go 包 | 小写单词，不用下划线、不用复数 | `credential` `ingestion` |
| 文件 | 小写下划线 | `credential_service.go` `order_status.go` |
| 导出类型/函数 | CamelCase | `CredentialService` `ApplyMapping` |
| 私有 | camelCase | `sealSecret` |
| 接口 | 名词或 `-er` | `Platform` `Refresher` `TokenIssuer` |
| DB 表 | 小写下划线，**单数** | `shop` `sales_order` |
| DB 列 | 小写下划线 | `external_order_id` |
| JSON 字段 | 小写下划线 | `external_order_id` |
| URL 路径 | kebab-case，资源用复数 | `/api/v1/admin/price-lists/{id}` |
| 错误码常量 | `Err` 前缀 + 域 + 语义 | `ErrShopNotFound` |
| 环境变量 | 大写下划线，`CH_` 前缀 | `CH_DB_DSN` |
| 队列任务名 | `域:动作` | `sync:run` `credential:refresh` |

---

## 2. HTTP API 协议

### 2.1 前缀划分

| 前缀 | 使用者 | 鉴权 |
|---|---|---|
| `/api/v1/admin/**` | 管理端 SPA | JWT + RBAC + 数据权限 |
| `/api/v1/share/**` | 客户 H5 | URL token（免登录） |
| `/api/v1/open/**` | 外部开发者 | HMAC 签名 + 授权 |
| `/api/v1/auth/**` | 登录相关 | 无 / refresh token |
| `/healthz` `/readyz` `/metrics` | 运维 | 内网 |

### 2.2 统一响应

```json
{
  "code": 0,
  "message": "ok",
  "data": {},
  "trace_id": "01HX...",
  "request_id": "optional-client-id"
}
```

- `code = 0` 表示成功；非 0 为业务错误码（见 §3）。
- HTTP 状态码同时具有语义：成功 200/201/204，参数错误 400，未认证 401，无权限 403，不存在 404，冲突 409，限流 429，服务端 500。
- **禁止**在成功响应里塞 `code: 200` 这类与 HTTP 重复的语义。
- 分页响应固定：

```json
{ "code": 0, "data": { "list": [], "total": 123, "page": 1, "page_size": 20 } }
```

### 2.3 分页与排序

- 请求参数：`page`（从 1 开始）、`page_size`（默认 20，最大 100）、`sort`（如 `-created_at`，`-` 表示倒序）。
- 只允许白名单字段排序，**禁止**把 `sort` 直接拼进 SQL。
- 超过 1 万条的遍历/导出走游标分页（`cursor` + `limit`）或异步导出，不用 `offset`。

### 2.4 时间与金额

- 时间入参出参一律 RFC3339 UTC：`2026-10-30T07:00:00Z`。
- 日期范围参数：`created_from` / `created_to`（含头不含尾）。
- 金额字段一律 `*_cents` 的 int64，并同时返回 `currency`。
- **禁止**字符串金额、浮点金额、科学计数法。

### 2.5 写接口规范

- 创建返回 `201` + 完整对象。
- 更新用 `PUT`（全量）或 `PATCH`（局部），必须带 `version` 做乐观锁；冲突返回 409 + `10409`。
- 删除用 `DELETE`，软删除，返回 `204`。
- 批量操作走 `POST /batch-*`，返回逐条结果：`{"success":10,"failed":[{"id":1,"code":...}]}`。
- 所有写接口支持 `Idempotency-Key` 头（可选），同键重复请求返回首次结果。

### 2.6 错误响应

```json
{
  "code": 30101,
  "message": "店铺不存在",
  "details": { "shop_id": 12 },
  "trace_id": "01HX..."
}
```

- `message` 是给人看的，**不要**把 SQL 错误、堆栈、上游原始报文放进去。
- 上游错误统一映射成平台无关错误码，原始信息进日志。

---

## 3. 错误码

格式：`<域><类型><序号>`，5 位数字。

| 段 | 含义 |
|---|---|
| 1 | 通用 / 系统 |
| 2 | 身份与权限 |
| 3 | 店铺与凭据 |
| 4 | 商品 |
| 5 | 订单 |
| 6 | 同步与映射 |
| 7 | 分享与报价 |
| 8 | 开放网关 |
| 9 | 计费 |

第二段：`0` 通用参数、`1` 不存在、`2` 冲突、`3` 状态非法、`4` 上游失败、`5` 限流。

### 3.1 通用（10xxx）

| 码 | 常量 | HTTP | 含义 |
|---|---|---|---|
| 10001 | `ErrInvalidParam` | 400 | 参数校验失败 |
| 10002 | `ErrInternal` | 500 | 内部错误 |
| 10003 | `ErrNotFound` | 404 | 资源不存在（通用） |
| 10004 | `ErrConflict` | 409 | 资源冲突 |
| 10005 | `ErrTooManyRequests` | 429 | 触发限流 |
| 10006 | `ErrIdempotencyMismatch` | 409 | 同幂等键不同请求体 |
| 10409 | `ErrVersionConflict` | 409 | 乐观锁冲突 |
| 10007 | `ErrUpstreamTimeout` | 504 | 上游超时 |

### 3.2 身份（20xxx）

| 码 | 常量 | HTTP | 含义 |
|---|---|---|---|
| 20101 | `ErrInvalidCredentials` | 401 | 用户名或密码错误 |
| 20102 | `ErrTokenExpired` | 401 | token 过期 |
| 20103 | `ErrTokenRevoked` | 401 | token 已失效 |
| 20104 | `ErrAccountLocked` | 423 | 账号锁定 |
| 20105 | `ErrRoleInUse` | 409 | 角色仍被引用 |
| 20106 | `ErrMenuCycle` | 400 | 菜单父子成环 |
| 20107 | `ErrPermissionDenied` | 403 | 无权限 |
| 20108 | `ErrDataScopeDenied` | 403 | 超出数据权限 |

### 3.3 店铺与凭据（30xxx）

| 码 | 常量 | HTTP | 含义 |
|---|---|---|---|
| 30101 | `ErrShopNotFound` | 404 | 店铺不存在 |
| 30201 | `ErrShopDuplicate` | 409 | 同平台店铺已存在 |
| 30301 | `ErrCredentialInvalid` | 400 | 凭据不可用 |
| 30302 | `ErrCredentialExpired` | 409 | 凭据已过期 |
| 30303 | `ErrCredentialNeedsReauth` | 409 | 需重新授权 |
| 30401 | `ErrPlatformUnsupported` | 400 | 平台未注册适配器 |
| 30501 | `ErrCredentialDecrypt` | 500 | 解密失败（密钥不匹配） |

### 3.4 商品 / 订单（40xxx / 50xxx）

| 码 | 含义 |
|---|---|
| 40101 | 商品不存在 |
| 40201 | 商品编码重复 |
| 40301 | 商品已被报价单引用，不可删除 |
| 40401 | 类目映射不存在 |
| 50101 | 订单不存在 |
| 50301 | 订单状态非法流转 |
| 50302 | 订单已关闭不可操作 |
| 50401 | 收货信息解密失败 |

### 3.5 同步与映射（60xxx）

| 码 | 含义 |
|---|---|
| 60101 | 同步任务不存在 |
| 60201 | 同一店铺资源已有运行中的任务 |
| 60301 | 映射版本不可修改 |
| 60302 | 映射表达式非法 |
| 60303 | 映射必填项缺失 |
| 60401 | 平台鉴权失败（需刷新凭据） |
| 60402 | 平台限流 |
| 60403 | 平台接口错误 |
| 60501 | 原始报文不存在 |
| 60601 | 重放范围非法 |

### 3.6 分享（70xxx）

| 码 | 含义 |
|---|---|
| 70101 | 分享链接不存在 |
| 70102 | 分享链接已失效（过期/超次数/撤销） |
| 70103 | 访问密码错误 |
| 70201 | 报价单不存在 |
| 70202 | 报价单已过期 |
| 70301 | 分享内容为空（无有效商品） |

### 3.7 网关（80xxx）

| 码 | 含义 |
|---|---|
| 80101 | 应用不存在或已停用 |
| 80102 | 签名错误 |
| 80103 | 时间戳超窗或 nonce 重放 |
| 80104 | 未授权访问该路由 |
| 80105 | IP 不在白名单 |
| 80201 | 路由不存在 |
| 80301 | 上游不可用 |
| 80302 | 上游超时 |

### 3.8 计费（90xxx）

| 码 | 含义 |
|---|---|
| 90101 | 钱包不存在 |
| 90102 | 余额不足 |
| 90103 | 账户欠费已停用 |
| 90104 | 冻结金额不足 |
| 90201 | 套餐已用尽 |
| 90301 | 兑换码无效或已使用 |
| 90401 | 对账差异 |

---

## 4. Go 代码规范

### 4.1 分层约束（重复强调，违反即打回）

```go
// ✅ 正确
func (h *ShopHandler) Create(c *gin.Context) {
    var req CreateShopReq
    if err := c.ShouldBindJSON(&req); err != nil { httpx.Fail(c, errs.Invalid(err)); return }
    out, err := h.svc.Create(c.Request.Context(), req)
    if err != nil { httpx.Fail(c, err); return }
    httpx.OK(c, out)
}

// ❌ 禁止：handler 里出现 db / gorm / http.Client
```

### 4.2 错误处理

- 业务错误用 `errs.AppError{Code, HTTPStatus, Message, Details, Cause}`。
- 用 `errors.As` 判断类型；**禁止**字符串比较错误。
- 只有「可恢复且需要上下文」的才 wrap：`fmt.Errorf("apply mapping: %w", err)`。
- 所有 error 返回路径必须被处理；禁止 `_ = err`（除显式忽略并注释原因）。

### 4.3 上下文与超时

- 所有跨进程调用必须接受 `ctx` 并设置超时：DB 3s、Redis 1s、平台 API 15s、上游代理按路由配置。
- 禁止使用 `context.Background()`，除 `main` 与后台任务入口。
- goroutine 必须有退出路径（`ctx.Done()` 或 `WaitGroup`），禁止裸 `go func()`。

### 4.4 事务

- 事务边界在 service 层，用 `db.Transaction(ctx, func(tx) error {...})`。
- 事务内禁止调用外部 HTTP、发消息、写日志以外的 IO。
- 领域事件用 Outbox 与业务同事务写入，事务提交后由投递器发送。

### 4.5 日志

- 统一 `slog`，结构化字段；禁止 `fmt.Printf`、`log.Println`。
- 必须带 `trace_id`；业务日志加 `module`、`action`。
- **禁止**记录：密码、token、secret、完整手机号、完整地址、原始报文全文（只记 raw_id）。
- 级别：`Error` 需人介入；`Warn` 可自愈；`Info` 关键业务节点；`Debug` 仅本地。

### 4.6 测试

- 表驱动 + `testify`。
- 单元测试不连真实 DB；repo 层集成测试用 `TEST_DATABASE_URL`，未设置则 `t.Skip`。
- 命名：`TestShopService_Create_DuplicateExternalID`。
- 每个 P0 需求至少 1 个测试，测试内注释引用 `REQ-xxx`。
- 禁止 `time.Sleep` 等待异步结果，用轮询 + 超时（`require.Eventually`）。

### 4.7 其他

- 单文件 ≤ 400 行，单函数 ≤ 80 行，圈复杂度 ≤ 12。
- 导出标识符必须有注释；包必须有 `doc.go` 或包注释。
- 禁止 `init()` 做重活（只允许注册）。
- 依赖注入手工装配（`internal/bootstrap`），不引入 wire/dig。
- 第三方库引入需在 PR 说明理由；禁止引入 ORM 之外的「全家桶」框架。

---

## 5. 数据库规范

- 迁移只增不改；已合并的迁移禁止编辑。
- 每个迁移必须提供可逆的 `down`（删表/删列；数据迁移类可写 `-- irreversible` 注释并只做 no-op）。
- 建索引用 `create index concurrently`（大表），迁移文件里单条语句。
- 禁止在应用里做 `select *`；GORM 查询必须 `Select` 显式字段（大字段如 `description` 单独查）。
- 批量写用 `CreateInBatches` / `ON CONFLICT`，单批 ≤ 500 行。
- 长事务禁止（> 2s 报警）。
- JSONB 字段查询必须建 GIN 索引，且用 `@>` / `jsonb_path_ops`，禁止 `->>` 全表扫。

---

## 6. Git 与协作

### 6.1 分支
- `main` 永远可部署。
- `feat/M3-sync-framework`、`fix/credential-decrypt`、`docs/prd-v1.1`。

### 6.2 提交

```
<type>(<scope>): <subject> [REQ-XXX-000]

<body: 为什么这么改>
<footer: BREAKING CHANGE / 关联 issue>
```

`type`：`feat` `fix` `refactor` `test` `docs` `chore` `perf` `build`。
`scope`：模块名（`shop` `sync` `gateway`）。

### 6.3 PR

PR 描述必须包含：

```markdown
## 对应需求
REQ-SYNC-003, REQ-SYNC-004

## 改动
- ...

## 验收
- [ ] make lint test
- [ ] 手动验证：<命令与输出>

## 偏差说明
- 无 / 或逐条列出与规格不一致之处及原因
```

**没有「偏差说明」小节的 PR 不予合并。**

### 6.4 禁止事项（Codex 硬约束）

1. 不得修改 `docs/01-PRD.md`、`docs/03-data-model.md`、`docs/ADR.md` 的既有结论（只能提 proposal）。
2. 不得擅自新增第三方依赖。
3. 不得修改已合并的迁移文件。
4. 不得修改任务卡范围外的文件。
5. 不得留 `TODO`、`panic("not implemented")`、空实现（除非任务卡明确要求占位）。
6. 不得为了让测试通过而放宽断言或删除测试。
7. 不得在代码里硬编码平台字段名（必须走映射配置）。
8. 不得把 secret 写入任何日志、错误、注释、测试夹具。

---

## 7. 配置文件

```yaml
# configs/config.example.yaml
app:
  env: dev            # dev / staging / prod
  name: commercehub
  http_addr: ":8080"
  read_timeout: 30s
  write_timeout: 30s

db:
  dsn: "postgres://user:pass@localhost:5432/commercehub?sslmode=disable"
  max_open: 50
  max_idle: 10
  conn_max_lifetime: 30m

redis:
  addr: "localhost:6379"
  password: ""
  db: 0

crypto:
  master_key: "base64:..."     # 32 字节，prod 必须来自 KMS/环境变量
  key_version: 1

storage:
  endpoint: "http://localhost:9000"
  bucket: "commercehub"
  access_key: ""
  secret_key: ""
  use_ssl: false

jwt:
  secret: ""                   # prod 必须覆盖
  access_ttl: 15m
  refresh_ttl: 168h

worker:
  concurrency: 10
  queues: ["sync", "refresh", "billing", "notify"]

log:
  level: info
  format: json
```

- 环境变量覆盖规则：`CH_DB_DSN` → `db.dsn`（`CH_` 前缀 + 下划线转点）。
- 启动时校验必填项，缺失直接 fail fast 并打印缺哪个。

---

## 8. 完成定义（Definition of Done）

一张任务卡算完成，必须全部满足：

1. 代码通过 `make lint`（golangci-lint，零告警）。
2. `make test` 全绿，新增逻辑有测试，P0 需求有对应用例。
3. 数据库迁移 `up`/`down` 各跑一遍通过。
4. 新增接口在任务卡接口表中有记录，且实际响应符合 §2。
5. 无新增 `TODO` / 空实现 / 调试代码。
6. 日志不含敏感信息（自查一遍）。
7. PR 描述含「对应需求」与「偏差说明」。
8. 如果是里程碑最后一张卡：更新该里程碑 `README` 的完成状态。
