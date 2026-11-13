# Backlog

| # | 发现于 | 问题 | 影响 | 建议里程碑 |
|---|---|---|---|---|
| 1 | M0 | `.golangci.yml` 的 `disable-all: true` 是 golangci-lint v1 语法，v2 会移除；升级时改为 `linters.default: none` | 当前可用，升级 v2 时需调整配置 | 升级 golangci-lint v2 时处理，M0 不修改 |
| 2 | M3b T3.7/T3.9 | 已修复：发布前将 `status_tables` 快照写入最终 spec，再计算 checksum，事务内存库后 CAS active；worker 从 DB spec 独立编译 | `CompileFromVersion` 对无快照的旧版本返回 `ErrStatusSnapshotMissing`，须重新发布；不回退当前 sys_setting，无需迁移 | 验证见 `MappingSnapshotIntegration`：真实发布、独立 worker 进程、状态表修改/不可访问、重启 checksum 一致及 CAS 回滚 |
| 3 | M4b2 第一轮 | `sizecheck.go` 发现既有 `internal/modules/identity/release.go:Apply` 为 93 行，超过单函数 80 行约束；本轮未改该文件 | 全库长度检查退出 1，本轮新增/修改函数通过 | identity 维护任务中拆分，不混入导出实现 |
| 4 | S3 冒烟 | worker 缺少 HTTP 就绪端点（`cmd/worker/main.go`、`internal/bootstrap/worker.go`） | 部署到 k8s/ECS 等编排系统时需要 `/readyz`，检查 Redis/DB 连通性与队列注册状态；本次按人工确认，使用进程存活 + Inspector `Servers()` 中该 PID 的 active 状态及四个队列精确集合，30 秒内每 500ms 轮询；属于真实就绪验证，不标 skipped，不阻塞 S3 | 独立运维任务，S3 不修改产品代码 |
| 5 | S3 冒烟 | API `/readyz` 需要 JWT（`internal/kernel/httpx/engine.go`），空 schema 尚无管理员时无法先检查就绪 | 按人工确认，第 2 步先 bootstrap seed/login 再检查真实 `/readyz`；第 3 步仍独立重复 seed 并验证管理员唯一与登录成功 | 运维鉴权方案独立评估，S3 不修改产品代码 |
| 6 | S3 冒烟 | S3 卡片第 13 步写撤销后 HTTP 403，现有 `share.AccessDenied` 契约为 HTTP 404 / 70102 | 按人工确认，精确断言 404 / 70102，并使用已获准入的浏览 Cookie 验证撤销立即生效；卡片与现有契约待对齐 | 文档契约对齐，S3 不修改产品代码 |
| 7 | S4 §3.2 / §5.7 | `auth_login.go:loginAudit` 未把 `RequestMeta.IP/UserAgent` 写入 `audit.Event`；真实未知账号登录回归证实查询结果均为空字符串 | 查询可保留已有 IP/UA，但当前登录链路新产生的历史也缺失来源信息；S4 按卡片保持写入逻辑不变 | 独立卡 FOLLOWUP-S4-LOGIN-METADATA，补全写入后再验证真实登录端到端来源信息 |

## FOLLOWUP-S4-LOGIN-METADATA · 登录审计来源信息补全（待排期）

- 来源：S4 §3.2 明确要求发现字段缺失时单独提卡。
- 范围：`internal/modules/identity/auth_login.go` 的登录审计调用链与对应测试；传递请求 IP、User-Agent，不新增表或迁移。
- 验收：通过真实登录接口分别产生成功、失败、未知账号记录，查询登录历史保留对应 IP/UA；未知账号仍为 `resource_id=unknown` / 「未知账号」，不得记录尝试输入的账号或凭据。
- S4 证据：`TestSystemAuditIntegrationLoginHistory` 验证已落库来源字段原样可查；`TestSystemAuditIntegrationUnknownLoginAndWriterCompatibility` 验证当前真实写入遗漏及匿名展示。
