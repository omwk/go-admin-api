# M8a 契约与密钥认证 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 在 feat/M8a-open-contract 上实现独立 /open/v1、受控密钥生命周期与 HMAC；完成真实订单状态查询的独立契约。

**Architecture:** gateway 定义应用主体、密钥与路由端口，bootstrap 实现主库适配和装配。公开响应只由独立 DTO 构造，管理端继续使用 M1 JWT/RBAC；order 提供应用专用只读端口。

**Tech Stack:** Go 1.23、Gin、现有 GORM/PostgreSQL、go-redis、crypto.Sealer；不新增依赖。

**Spec:** docs/milestones/M8.md；docs/ADR.md ADR-022；docs/proposals/M8-review.md Q1/Q2/Q6/§7；本次 M8a 指令。

## Global Constraints

- 不做计量、不做限流、不做 M8b；api_call_log 仅建表。
- 独立前缀 /open/v1，代码能力登记 + 主库 api_route enabled/disabled 双门禁；未知/停用均 404/80201。
- 签名六段 METHOD、原始 PATH、canonical_query、timestamp、nonce、hex(sha256(body))，换行连接。
- 时间窗 ±300s；nonce 按应用共享，先 SETNX 再验签，TTL 600s，验签失败保留；Redis/主库故障 503/10002。
- secret 为 crypto/rand 32 字节的标准 base64 文本；HMAC 使用文本字节；库中仅指纹、前缀、绑定 tenant/client/secret/version 的受控密文。
- 最多两把 active key；轮换默认 24h，最长 7d；不延长旧 key，吊销与到期在主库即时拦截。
- 认证失败均 401，文案统一“应用凭证不可用”，保留 §7 的分类 code；不泄露命中 key 或失效原因。
- 独立公开 DTO/OpenAPI；字段白名单；details 不传播内部值；不暴露 tenant/shop/平台实例 ID、成本或 PII。
- 不修改 M1 会话语义；不伪造 AdminActor；不隐式回源、不刷新平台凭据。
- 迁移取当前最大号 +1（目前 000017），只增不改；up/down/up；不新增第三方依赖。
- 集成测试 TestGateway...Integration...，在 scripts/check-test-groups.ps1 中恰好归属一个分片。
- 新增 Go 文件 ≤400 行，函数 ≤80 行；不将 Go/lint 缓存放进仓库。
- 订单资源方案已获本次会话明确批准（选择 A）：新增 `api_client_resource_grant`，逐订单授权并生成独立公开 ULID；应用上限与 owner 当前订单权限/店铺范围取交集。

### Task 1: 主库模型、密钥生命周期与认证内核

**Files:**
- Create: migrations/000017_gateway_init.up.sql / .down.sql。
- Create: internal/modules/gateway/ 中 doc.go、types.go、secret.go、auth.go、signature.go、errors.go 及职责对应测试。
- Create: internal/bootstrap/gateway_store.go、gateway_secret.go、gateway_migration_integration_test.go、gateway_secret_integration_test.go。
- Modify: scripts/check-test-groups.ps1（新增 gateway 分片规则）。

**Interfaces:** gateway 自有应用、密钥和路由类型，不导入其他模块 repo/model；生命周期接口应覆盖创建应用、签发/轮换/吊销密钥、停用应用，以及主库每次加载当前状态。后续 HTTP 层只依赖这些公共服务。

- [x] 增加协议、生命周期单测和真实主库集成测试；focused gateway/bootstrap 测试通过。
- [x] 新增 api_client（ULID app_id、tenant、owner、name、status、精确 IP 白名单、version/时间）、api_client_secret（fingerprint、secret_prefix、cipher/nonce/key_version、status、expires_at、last_used_at）、api_route（code、capability、method/path、group、enabled/disabled、visibility、version）、api_client_route；建立 api_call_log 月分区空表。现有表和迁移保持不动。
- [x] 实现随机 secret、Sealer 信封与用途 AAD；用应用行锁串行密钥创建，至多两把 active；到期判断不依赖清理；轮换不延长旧 key；事务审计不含秘密。
- [x] 实现 HMAC 六段串、原始 path、排序 query；拒绝重复键/非法编码；限制认证头格式与 body 1MB。固定最多两把验签，用 hmac.Equal；SETNX 早于验签，应用共享 nonce。
- [x] 测试正确/错误签名、空体/query、空值、+/百分号/UTF-8、篡改 path/query/body、±300s 边界、过期/replay/revoked/suspended、两 key、AAD 调包、Redis/DB 故障、并发至多两把、旧 key 到期。
- [x] focused unit/integration 和分片守卫通过；保持本地未提交，随本次 M8a 一并交付。

### Task 2: 独立 open 栈、管理生命周期与订单契约

**Files:**
- Create: internal/modules/gateway/ 下 registry.go、open_http.go、admin_http.go、admin_types.go、openapi.yaml 及测试，按文件职责拆分。
- Create: internal/bootstrap/gateway.go、gateway_routes.go、gateway_order.go、gateway_resource.go、gateway_http_integration_test.go 及 fixture；公开契约守卫位于 gateway/open_http_test.go。
- Create: internal/modules/order/open_status.go；通过 bootstrap 真实订单集成测试验证领域端口。
- Modify: internal/bootstrap/identity.go（管理装配入口）、internal/modules/identity/permission/registry.go（集中 gateway 权限）、docs/milestones/M8.md（M8a 接口表及验收）。
- 必要时新增独立 open 发布配置/入口适配与守卫，参考 M6 PublicIngress 的纪律；不将 open 混入 share allowlist。

**Interfaces:** 消费 Task 1 gateway 公共服务；管理身份只来自现有 M1 中间件。订单公开端口接受经过主库验证的应用主体及公开订单标识，返回独立公开事实。

- [x] HTTP 黑盒测试：未知与 disabled 路由相同 404/80201、正确签名可达登记能力、未授权 403/80104、admin 不受 open HMAC 影响。
- [x] 实现显式 registry 与独立 dispatcher；DB 只能启停代码登记能力，每次 dispatch 主库读取状态。仅首条订单状态能力，拒绝任意内部 handler、paid/private/未知能力。
- [x] 实现受 JWT/RBAC/范围约束的应用、密钥、路由启停和应用路由授权管理接口；secret 初次返回 no-store，列表/错误/幂等回执不包含明文。
- [x] 按获批方案新增授权表、应用领域只读端口与严格 tenant/client/resource/owner 范围交集；不复用人类 order.QueryService；公开 id 不使用内部 ID。
- [x] 定义 additionalProperties=false 的独立 OpenAPI，成功 DTO 逐字段构造；§7 错误映射只输出安全固定文案，details 省略。trace/request ID 有长度字符限制。
- [x] 增加资源跨应用/租户/owner/授权失效矩阵、内部标记扫描、OpenAPI/注册表字段守卫及 dispatcher 管理入口隔离测试。
- [x] focused 验证通过并审阅实际 diff；已更新接口表与实现状态，不触碰 M8b。

### Task 3: 总验收与交付

**Files:** docs/milestones/M8.md 的 M8a 验收记录；必要的 M8a 修正文件。

- [x] 执行 powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1 -Integration -Migration，退出码 0，末行 all checks passed；完整验收摘要见 M8.md §8。
- [x] 分片守卫通过（Gateway 11；Source 303 = shards 302 + excluded 1）；TODO/空实现、生产路径调试日志/内部错误详情/background context 扫描通过；32 个改动 Go 文件最多 261 行、函数最多 80 行；git diff --check 通过。
- [x] 审阅任务 diff，修正管理装配缺省 keyring 兼容性及测试夹具；最终完整验证包含这些修正并通过。
- [x] 整理完成项、改动、验收命令/输出和偏差；停在 M8a，本地未提交/未推送，未开启 M8b、未打 tag、未合并。
