# BE-IDN-009 验收证据

验证日期：2026-11-16。分支 `feat/be-idn-009-audit-semantics`，起点 `2567c3464cfdf6c260f7a46dc12b3e9fd622dbeb`。

后端实现及自动化检查完成。第 17 项的实际历史资源名称覆盖率为 **84.91%**，低于 90%；无法关联现表的记录按任务卡保持 null。操作者名称覆盖率为 98.94%，租户名称覆盖率为 100%。不能将隔离夹具的 96% 当作现有历史的覆盖率。

## 十八项验收

| # | 验收证据 | 对应检查 |
|---|---|---|
| 1 | 一次跨 12 个租户的 GET：`total before=1 after=2 delta=1`。 | `TestIdentityAuditSemanticsIntegrationReadPerRequest` |
| 2 | 同条记录 `before=null`；`accessed_tenants` 恰好 12 个外部租户，`count=12`，不含本租户。 | 同上，解析持久化 JSON 并逐项断言 |
| 3 | 普通用户读取本租户列表：`audit delta=0`。 | `TestIdentityAuditSemanticsIntegrationOwnTenantNoAudit` |
| 4 | 只改名称：before 恰好一个 name 字段，after 恰好为新 name。 | `TestIdentityAuditSemanticsIntegrationChangedFieldsAndNoop` |
| 5 | 相同名称再次 PUT：审计数量不变；相同 sync 配置两次 PUT 也只有一条。 | 同上；`TestSystemSyncSettingsIntegrationUpdateAndAudit` |
| 6 | 模板新建 before=null、after 非空；物理删除 before 非空、after=null。显式删除标记即使保留相同 before/after 也不会被误判为无变化。 | `TestIdentityAuditSemanticsIntegrationImmutableNamesAndDeletion`；`TestAuditSemanticsExplicitDeletionKeepsBeforeSnapshot` |
| 7 | HTTP 写入审计保存 `ip=192.0.2.9`、`user_agent=audit-semantics-integration`。 | `TestIdentityAuditSemanticsIntegrationChangedFieldsAndNoop`；来源为测试请求头和 RemoteAddr |
| 8 | 实际历史副本迁移后 1,796 条，`colon=0 invalid=0`；写入层也验证 action 格式。 | 实际历史副本统计；`audit.Record` 的正则校验 |
| 9 | 原历史包含 610 条冒号 action；迁移副本前后均为 1,796 条，迁移后冒号为 0。 | 下文“实际历史迁移统计” |
| 10 | 登记 83 个 action；一致性测试通过；临时加入 `system.tenant.unregistered` 时测试以未登记错误失败，移除后通过。 | `TestAuditEventsRegistry`，负向探针未保留在仓库 |
| 11 | 真实 CLI：prod 无 force 拒绝；带 force 输出 `deleted=1`，截止点及之后剩 2 条。外层事务失败会恢复删除，普通 UPDATE/DELETE/TRUNCATE 仍被拒绝。 | `TestIdentityAuditPruneCLIIntegrationCutoffAndForce`；`TestIdentityAuditSemanticsIntegrationPruneBoundaryAndRollback` |
| 12 | 写时名称为 `admin / default / original-label`；查询直接返回保存的三个快照列。 | `TestIdentityAuditSemanticsIntegrationImmutableNamesAndDeletion`；`TestIdentityAuditSemanticsIntegrationSearchSnapshots` |
| 13 | actor username、tenant code 改名后，旧记录仍为 `admin / default`；HTTP 查询同样验证操作者快照不变。 | 同上 |
| 14 | 资源改名并物理删除后，旧创建记录仍为 `original-label`，删除记录为 `renamed-label`。 | `TestIdentityAuditSemanticsIntegrationImmutableNamesAndDeletion` |
| 15 | 失败登录保存 `20101 / 用户名或密码错误`；任意底层错误保存 `10002 / 操作失败`，不存任意 cause 文本。失败事件不因状态相同而被抑制。 | `TestIdentityAuditSemanticsIntegrationFailureReason`；`FailureSanitizedAtSink`；kernel audit 单元测试 |
| 16 | `keyword=ADMIN` 命中操作者；`action_prefix=system.tenant.` 只命中该前缀；精确 actor_name、字面量通配符转义及租户隔离均通过。 | `TestIdentityAuditSemanticsIntegrationSearchSnapshots` |
| 17 | 实际历史副本：actor **1777/1796=98.94%**；tenant **1796/1796=100%**；resource **1525/1796=84.91%**。资源列未达到 90%，如实保留偏差。 | 下文分组统计；隔离迁移夹具另为 24/25=96%，不替代实际统计 |
| 18 | `scripts/verify.ps1 -Integration` 返回 0，末行 `all checks passed`。 | vet、lint、`go test ./...`、完整 integration |

## 命令与输出

本机没有 make，使用项目自带 verify 执行 Makefile 中等价的 `golangci-lint run`、`go test ./...`，并执行 vet 与集成测试。

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1 -Integration
```

关键输出：

```text
== go vet ==
== golangci-lint ==
== go test ==
ok  github.com/omwk/go-admin-api/cmd/cli 45.197s
ok  github.com/omwk/go-admin-api/internal/bootstrap 381.510s
ok  github.com/omwk/go-admin-api/internal/kernel/audit 0.411s
ok  github.com/omwk/go-admin-api/internal/kernel/db 14.972s
ok  github.com/omwk/go-admin-api/internal/modules/identity (cached)
ok  github.com/omwk/go-admin-api/internal/modules/system 1.344s
== integration ==
ok  github.com/omwk/go-admin-api/internal/kernel/db 7.947s
ok  github.com/omwk/go-admin-api/internal/bootstrap 365.807s
ok  github.com/omwk/go-admin-api/cmd/cli 32.015s
all checks passed
```

HTTP 写操作 IP/UA、迁移覆盖率和显式删除标记均已纳入最终完整 verify。另有以下针对性验收：

```powershell
go test ./internal/bootstrap ./internal/kernel/db ./cmd/cli ./internal/kernel/audit -run 'IdentityAuditSemanticsIntegration|IdentityAuditPruneCLIIntegration|MigrateAuditSemanticsIntegration|TestAuditEventsRegistry|TestAuditSemantics|TestAuditPruneArgumentsAndProductionGuard|TestIdentityTenantIntegrationAuditFailureRollsBack' -count=1 -v
```

```text
ok  github.com/omwk/go-admin-api/internal/bootstrap 6.141s
ok  github.com/omwk/go-admin-api/internal/kernel/db 3.108s
ok  github.com/omwk/go-admin-api/cmd/cli 2.699s
ok  github.com/omwk/go-admin-api/internal/kernel/audit 0.541s
```

显式删除兼容回归执行了负向与正向验证：通过临时 Go overlay 去掉 `AfterNull` 防护后，测试按预期失败；仓库源码保持不变。正常源码执行如下命令通过：

```powershell
go test ./internal/kernel/audit -run '^TestAuditSemanticsExplicitDeletionKeepsBeforeSnapshot$' -count=1 -v
```

```text
--- PASS: TestAuditSemanticsExplicitDeletionKeepsBeforeSnapshot (0.00s)
PASS
ok  github.com/omwk/go-admin-api/internal/kernel/audit 0.071s
```

审计失败回滚测试通过；已有越权 404、幂等、物理删除、授权和设置集成测试均在全量校验中执行。未删除测试或降低断言；旧测试中关于冒号 action、重复值也记审计、未变化上下文字段的预期按本卡改为新的明确断言。

## 实际历史迁移统计

从 `.env` 所配置测试库读取当前 **1,796 条**历史。在 REPEATABLE READ 事务内创建独立 schema，将 `public.audit_log` 复制为该 schema 的 audit_log，再执行 000031 的事务体。其他实体仅从 public 读取；原 audit_log 未执行更新。最后 ROLLBACK，并确认临时 schema 已不存在。没有迁移或清理原库。

```text
actual configured-test history clone migration: before=1796 after=1796 colon=0 invalid=0
actor_name=1777/1796 (98.94%); actor_tenant_name=1796/1796 (100.00%); resource_label=1525/1796 (84.91%)
ROLLBACK verified: evidence schema removed; source history unchanged
```

副本执行迁移后的查询：

```sql
SELECT count(*) AS total, count(actor_name) AS actors,
       count(actor_tenant_name) AS tenants, count(resource_label) AS resources,
       count(*) FILTER (WHERE action LIKE '%:%') AS colons,
       count(*) FILTER (WHERE action !~ '^[a-z]+\.[a-z.]+$') AS invalid
FROM audit_log;
SELECT resource_type, count(*) AS total, count(resource_label) AS named
FROM audit_log GROUP BY resource_type ORDER BY resource_type;
```

| resource_type | 历史条数 | 有 resource_label | 未关联 |
|---|---:|---:|---:|
| dept | 37 | 0 | 37 |
| dict | 31 | 19 | 12 |
| dict_item | 30 | 18 | 12 |
| invite | 6 | 0 | 6 |
| menu | 43 | 1 | 42 |
| notification | 1 | 0 | 1 |
| role | 137 | 5 | 132 |
| setting | 27 | 27 | 0 |
| tenant | 707 | 707 | 0 |
| tenant_template | 13 | 3 | 10 |
| user | 764 | 745 | 19 |
| 合计 | 1796 | 1525 | 271 |

主要缺口来自当前实体表无法关联的历史对象，以及 invite/notification 等未配置名称关联的资源类型。遵守“回填不到保持 null”，没有使用当前其他对象的名称替代，也没有在查询时回查实体表。

独立迁移测试另外验证：31→30 后写入 25 条历史夹具，再 30→31，行数仍为 25；24 条可解析记录正确回填，1 条缺失对象保持 null；恢复 append-only 触发器后 UPDATE/DELETE/TRUNCATE 均失败。

全链迁移由 `TestMigrateIntegrationReversibleAndRepeatable` 执行：up、重复 up、version、连续 down 到 0、再 up 到 31；并发启动及关键结构不变量也随 db integration 执行。历史 action 转换不反向恢复旧拼写，down SQL 已明确标注。

最终迁移专项复验：

```powershell
go test ./internal/kernel/db -run 'TestMigrateAuditSemanticsIntegrationBackfillAndAppendOnly|TestMigrateIntegrationReversibleAndRepeatable' -count=1 -v
```

```text
--- PASS: TestMigrateAuditSemanticsIntegrationBackfillAndAppendOnly (3.42s)
--- PASS: TestMigrateIntegrationReversibleAndRepeatable (3.60s)
PASS
ok  github.com/omwk/go-admin-api/internal/kernel/db 7.812s
```

## DoD 自检

- lint / test：上述完整 verify 返回 0；迁移 up/down 使用隔离库测试，不在原库运行回滚。
- 查询契约：新增 keyword / actor_name / action_prefix 已记录在原任务卡 §3.7，五个返回字段在 §3.6；同步 `api/openapi/system.yaml`，HTTP 集成测试验证契约。
- `rg -n 'TODO|not implemented' internal`：无匹配。
- `git diff --check`：无输出。
- `rg -n 'audit\.Record\(' internal cmd`：生产代码仅 bootstrap 公共写入器一处，其他持久化入口均经过名称快照。
- 日志与敏感信息：检查改动、写入前递归脱敏及错误白名单；单测直接检查传给 SQL 的参数，集成测试检查持久化错误与安全查询投影。密码、令牌、密文、手机号和地址值按敏感键替换为 `***`，错误不保存底层 cause。
- 尺寸：对所有新增/改动 Go 文件执行 go/parser AST 检查，输出 `all changed Go files <=400 lines; functions <=80 lines`。
- 分片守卫：`powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-test-groups.ps1`，输出 `Source 555 = shards 554 + excluded 1`、`PASS: every integration test has exactly one shard or an explicit exclusion.`。
- 新增第三方依赖：无；前端改动：无。

尺寸检查命令（检查脚本保存在本机临时目录，不作为产品文件提交）：

```powershell
$files=@(git -c core.safecrlf=false diff --name-only -- '*.go' 2>$null)+@(git ls-files --others --exclude-standard -- '*.go')
go run "$env:TEMP\be-idn-009-sizecheck.go" -- $files
```

没有 HTTP 请求的后台任务不伪造 IP/UA；未知登录账号不保存输入账号名。名称按 Unicode 字符截断至 128。写入审计失败继续回滚业务，保留越权 404。

本机 CGO_ENABLED=0、无 gcc，未运行 race；合并前仍需 CI 的 race 结果。本次未推送、创建 PR 或合并。
