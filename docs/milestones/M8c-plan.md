# M8c 加固与验收 Implementation Plan

> **For agentic workers:** Use inline execution with verification-before-completion. 本次在既有功能分支完成后停下报告。

**Goal:** 交付真实可执行的签名示例、三项应急演练、隔离压测与 M8 逐项验收证据。

**Architecture:** 沿用 M8a/M8b 生产实现，不改表、错误码或 API。S3 在隔离数据上经管理端创建应用和授权，独立示例签名后调用订单状态并查询真实 usage。性能入口显式启用，以真实 PostgreSQL/Redis 测量完整 open 栈；业务读在计时外取得真实快照，两条 HTTP 路径复用该快照。

**Tech Stack:** 现有 Go、PowerShell 5.1、bash/curl/OpenSSL、PostgreSQL、Redis；无新增依赖。

**Spec:** M8.md、ADR-022 1–10、M8-review Q1–Q6/§7、本次 M8c 指令。

## Global Constraints

- M8 只计量不扣费；仅已登记订单状态公开能力；不启动 M9。
- secret 仅来自随机签发并通过内存/环境传递，不写输出或磁盘。
- nonce 先占位再验签，±300s、TTL 600s、两 key 共享 nonce/限额。
- 使用现有 Retry-After 与 UTC 日/月边界表达复位时间，不新增响应字段。
- 压测显式开关，普通 CI 不做容量/统计判定；缓存保持在仓库外。
- 新 Go 文件 ≤400 行、函数 ≤80 行；先读再改，无第三方依赖或迁移改动。

### Task 1: 签名示例与协议文档

**Files:** scripts/gateway/client/*.go、scripts/gateway/example/main.go、scripts/gateway/examples/order-status.sh、docs/gateway/*.md。

- [x] 编写独立客户端签名器，按六段串、Go QueryEscape、UTF-8、重复 query 拒绝实现；环境读取密钥，错误不回显输入。
- [x] 用随机 key 对照 gateway.CanonicalRequest 和真实 Authenticator，覆盖空体、编码、非法 query 与错误 key。
- [x] 加入 bash curl 实例和 Go SDK 风格命令；示例文件是文档的可执行来源。
- [x] 文档写明当前路径、DTO、重试/计量、主库吊销、错误映射与运行手册。

### Task 2: S3 与三项演练

**Files:** scripts/smoke.ps1、scripts/smoke/gateway.ps1、scripts/smoke/README.md、scripts/smoke/support.ps1、scripts/smoke/main.go、scripts/smoke/transport.go、scripts/gateway/{rotation,revocation,quota}.ps1。

- [x] 复用管理 API 创建应用、order.status 路由及逐订单授权；第 15 步签名→200→usage/meter/log 对账。
- [x] 轮换用短窗口，在窗口内验证两 key，轮询主库截止时间后验证旧 key 401、新 key 200。
- [x] 吊销演练预热旧 key，提交吊销后的第一次请求断言 401，再验证替代 key。
- [x] 日/月配额各验证 N/N+1、429/10005、limit_type、Retry-After 与 UTC 复位，usage 与持久准入数不超发。
- [x] 分别执行三个脚本；全链 15 步通过，失败路径也清理隔离资源。

### Task 3: 显式压测与瓶颈分析

**Files:** internal/bootstrap/gateway_benchmark*_test.go、scripts/gateway/benchmark.ps1、docs/milestones/M8c-performance.md。

- [x] gatewaybench build tag + 显式环境开关；使用真实 PostgreSQL 和专属 Redis DB，固定样本数及并发阶梯。
- [x] 相同真实快照端口：直连内部测试接口与 open 栈；记录 p50/p95/p99、配对差值与成功/失败/QPS。
- [x] 单独采样 HMAC、nonce Redis、限流 Redis、认证含主库、计量 DB 阶段，报告样本、配置与测量边界。
- [x] 输出脱敏 JSON 和报告；超过 20ms 保留实测并定位瓶颈及后续方案，不削弱持久化/门禁。

### Task 4: 验收收尾

**Files:** docs/milestones/M8c-acceptance.md、docs/milestones/M8.md、docs/progress.md。

- [x] 执行 verify.ps1 -Integration -Migration、分片守卫、长度/敏感输出/空实现与 whitespace 检查。
- [x] REQ-GW-001–009 逐条给证据及已存在边界，原规划 HTTP 代理/管理日志/routes 缺口如实记录。
- [x] 更新 M8 完成状态与遗留；记录准确验收输出及压测结论，停下报告。
