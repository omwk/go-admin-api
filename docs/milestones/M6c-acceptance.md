# M6c 验收记录

分支 `feat/M6c-inquiry-hardening`，基线 `5b65f76`。新增迁移 `000014_share_privacy`；没有新增依赖，没有修改既有迁移、PRD、数据模型或 ADR 结论。实现、测试和说明均属于 M6c。

## 已验证行为

以下输出来自实际隔离 PostgreSQL 测试；测试使用运行时生成合成 canary，不使用真实联系方式。命令按测试域可独立复现。

```powershell
go test ./internal/bootstrap -run '^TestShare(Inquiry|Privacy|Visit|Lifecycle|Exposure).*Integration' -count=1 -p 1 -v
go test ./internal/modules/share/... ./internal/kernel/httpx ./internal/kernel/config ./cmd/share-proxy -count=1
```

| 验收项 | 结果 / 对应证据 |
|---|---|
| 正常询价 | `201 submitted=true; six field envelopes verified`；DB 六个独立信封，无提交值明文；正常密钥/AAD 可解开，跨 tenant/id/field/revision/order 域均失败。 |
| 匿名回查 | GET inquiry、inquiry/1 和管理 inquiry 均拒绝；匿名没有联系人或历史状态接口。 |
| 来源防刷 | `source A submissions 1/2/3=201, 4=429; source B=201; replay=201`；变更 X-Forwarded-For 不能绕过；错误 Origin、缺 Cookie、非成员、体积、关闭 allow_inquiry 统一安全文案。 |
| 体积/挑战 | 最小完成时间、honeypot、声明长度与 chunked 超 1MiB 均安全 404；未产生询价。严格 16KiB JSON，不允许未知 owner/customer/status 字段。 |
| 幂等/原子性 | 相同键相同规范化输入只产生一条 inquiry/inbox/outbox/receipt；异内容 `409/10006`；过期回执可重用；inbox 或提交审计故障 503 且整笔回滚。审计恢复后同键重试仅提交一次。管理状态幂等重放重新检查范围。 |
| 掩码/权限/重认证 | `masked list; permission-only=403; permission+recent=200; recent-only=403; neither=403; age=5m denied`。 |
| 审计 | `five safe who/record/fields/time audits`；逐次成功/失败记 actor、inquiry 引用、fields、purpose、created_at、result，无明文/IP/UA；审计失败与任一字段损坏均不披露部分 PII。 |
| 范围与转化 | 当前归属即时限定列表总数和明文读取；客户移交后原负责人失去访问。首次 won 的 converted_at 不续期，终态不可重开。 |
| 180/30/180 天 | `unconverted=180d and converted=30d erased; one-second-fresh rows retained; terminated customer=180d erased`；擦除后信封不可解密，安全元数据保留。 |
| 每日清理/重试 | 同一天只有一个完成记录；审计故障回滚擦除；部分清理失败重试保留累计计数，完成后才写完成水位。 |
| 三年安全审计 | 经确认按 B 实现。仅明确列出的分享事件与资源类型组合满三年可删除，数据库时钟为准；到期包含边界，提前一微秒保留。未到期、其他域、未知事件、错误资源类型均不可删除，UPDATE/TRUNCATE 继续拒绝；混合删除整笔回滚，审计落库失败也回滚删除。仅剩审计的租户同样清理。 |
| 访问最小化/90 天 | `visit stores time/result/link only, IP hash=NULL and no UA`；90 天到期删除，提前一秒保留；root 与 visit 同事务，写失败则 view_count 仍为 0。读取需独立权限、范围、重认证和逐次审计。 |
| 生命周期 | customer disabled/deleted/transferred 与 quote disabled/deleted 全部 `cached link=404/revoked, unrelated link=200, safe audit persisted`；审计故障父对象变更/撤销回滚；隐式价格来源同样撤销，父对象启用不复活旧链。 |
| 公网暴露 | 真实 `cmd/share-proxy` 读取正式允许清单，四路径均 404；精确允许的四条分享操作可以实际转发；方法/编码/尾斜杠/query 变体上游零命中。 |
| 应用层认证 | `application: four system routes anonymous=401, live JWT=200, revoked JWT=401`；无认证装配时 403，执行依赖探针前拒绝。 |
| Canary | 数据库全行 JSON、应用日志、实际分享 SQL 参数化模板、错误、掩码列表、CSV、inbox、outbox、幂等回执均不含合成提交标记。SQL 证据同时要求实际观察到 inquiry/notification 的 INSERT，避免空日志误通过。 |
| 契约 | 两份 OpenAPI 与实际路由和权限表双向核对，匿名路由恰为四条；Visit/InquiryView/Submission/InquiryItem 属性与 Go DTO 精确一致。 |

公网核对命令与实际输出：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-share-exposure.ps1 -BaseUrl http://127.0.0.1:18081
```

```text
PUBLIC GET /healthz -> 404
PUBLIC GET /readyz -> 404
PUBLIC GET /metrics -> 404
PUBLIC GET /api/v1/admin/system/info -> 404
PASS: all four system routes reject anonymous access.
```

## 完整验收状态

2026-11-09 已确认 B，授权仅清理到期分享安全审计。新增迁移 14 将原触发器拆为 UPDATE/TRUNCATE 的全表禁止与 DELETE 的逐行保留判定，没有编辑迁移 000001。新函数与清理查询共享同一显式事件/资源允许清单，使用 UTC 数据库事务时钟判定满三年，应用时钟偏快不能提前删除。Down 恢复原有全表 UPDATE/DELETE/TRUNCATE 禁止；14→13→14 行为回归已通过。业务夹具的未来时钟不再用作审计到期基准，保留原有删除与失败回滚断言。

2026-11-09 最终完整 `scripts/verify.ps1 -Integration -Migration` 退出 0，全部通过。该入口先执行 vet/lint 与全仓测试，再独立执行未缓存的 Integration，最后实际执行迁移 up/down/up；以下均为实际输出。

```text
go test ./internal/bootstrap -run '^TestShare(Inquiry|Privacy|Visit|Lifecycle|Exposure).*Integration' -count=1 -p 1 -v
PASS
ok   internal/bootstrap      15.658s

powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1 -Integration -Migration
== go vet ==                 PASS (exit 0)
== golangci-lint ==          PASS (exit 0)
== go test ==                PASS
ok   cmd/cli                 12.952s
ok   internal/bootstrap     158.867s
ok   internal/kernel/db       5.697s
== integration ==           PASS
ok   internal/kernel/db       4.038s
ok   internal/bootstrap     154.864s
ok   cmd/cli                  9.064s
== migrate up/down/up ==
version=14 dirty=false
version=13 dirty=false
version=14 dirty=false
all checks passed
```

verify 实际执行的迁移命令，三个均退出 0：

```text
go run ./cmd/cli migrate up:   version=14 dirty=false
go run ./cmd/cli migrate down: version=13 dirty=false
go run ./cmd/cli migrate up:   version=14 dirty=false
```

独立检查：

```text
scripts/check-test-groups.ps1:
trade shard total: 55 integration tests
Source 255 = shards 254 + excluded 1
PASS: every integration test has exactly one shard or an explicit exclusion.

Changed Go files=63; max file=238; max function=80
M6 task card lines=396
rg -n 'TODO|not implemented|fmt\.Println|log\.Print' internal/: no matches
git diff --check: exit 0
```

长度核对使用 `git diff --name-only -- '*.go'` 与 `git ls-files --others --exclude-standard -- '*.go'` 的并集，PowerShell `Get-Content` 统计行数，并匹配顶层 `^func ` 至 `^}` 统计函数长度。定向 `TestShareInquiryIntegrationAtomicAuditFailure` 实际通过，输出 `submission audit failure=503 rolls inquiry/receipt/inbox/outbox back; same-key retry commits once after recovery`。另外不使用测试缓存重跑分享模块、HTTP/配置/反代单测，全部通过。

## 自检

- [x] lint：仓库 verify 执行 `golangci-lint run`，退出 0，最终 `all checks passed`。
- [x] test：`go test ./...` 通过，bootstrap 158.867s；独立 Integration 通过，bootstrap 154.864s。
- [x] 迁移：上列 up/down/up 为 14/13/14，均 `dirty=false`；隔离库另验证全链和回滚后的结构与审计行为。
- [x] 接口：M6 §6 与两份 OpenAPI 已记录；路由、权限、DTO 契约测试通过。
- [x] 无 TODO/空实现：上列 `rg` 命令无匹配。
- [x] 日志无敏感信息：应用与 SQL 参数模板动态 canary，加上 DB/错误/CSV/inbox/outbox/receipt 扫描；生产 SQL 日志关闭，访问日志仅路由模板。
- [x] 长度：63 个改动 Go 文件，最长 238 行、函数最长 80 行；上列命令已验证。

M6c 已完成，M6 到此结束，没有启动 M7。

## 偏差与运行边界

- 本机无 make，使用仓库 verify 入口的相同底层 `golangci-lint run` / `go test ./...`；未将 Go/lint 缓存指向仓库。
- 本机 `CGO_ENABLED=0`，不声称执行 race；合并前仍需 CI race。此轮未推送、创建 PR、合并、打 tag 或启动 M7。
- 公网结果来自正式公开配置的本机 HTTP 执行；真实生产域的 DNS/TLS/CDN/防火墙须按 [暴露面清单](M6c-exposure.md) 外网复验。
- PII 擦除为覆盖成不可解密信封，保留业务引用；不声称清除尚在轮换期的离线备份。公开媒体与已下载内容不可回收。
