# BE-IDN-015 · 投递记录可读与可操作

- ID: BE-IDN-015
- 类型: 修复
- 仓库: 后端
- 状态: 已实现，验收通过（2026-11-18）
- 预计: 1 人日
- 分支: `feat/be-idn-015-delivery-readability`
- 依赖: 无

---

## 1. 现状

投递记录页显示的字段对管理员几乎没有信息量：

| 列 | 现在 | 问题 |
|---|---|---|
| 模板 | `identity.verify` | 模板代码，不是人看的中文名 |
| 收件人 | `hash: —` `域: —` | 只存了哈希；未发送时全空，管理员无法判断发给谁 |
| 状态 | `待发送` / `已发送` | 尚可，但缺失败/跳过 |
| 失败原因 | `message_unavailable` | 原始错误码，不是人话 |
| 操作 | 空 | 失败后无法重试 |

### 1.1 为什么会发生「邮件没发出去」

实测：本地只启动了 `cmd/api`，没起 `cmd/worker`。

```
notify_outbox: 49 条 pending
notify_delivery: 全部 pending，attempts=0
```

worker 启动后立刻消化：3 条 sent、51 条 failed（旧事件 payload 已过期，
报 `message_unavailable`）。

**这是启动流程缺陷，不是代码缺陷**——但暴露了可用性问题：
投递记录页没有「为什么卡住」的任何线索。

## 2. 改动

### 2.1 模板显示名

投递记录返回 `template_name`（join `notify_template` 按 `template_code`），
模板已删除时回退 `template_code`。

### 2.2 收件人：存掩码，显示掩码

现状只存 `recipient_hash` + `recipient_domain`，**不可逆，管理员无法核对**。
新增 `recipient_masked`（如 `a***@163.com`、`138****1234`）：

- 写入时按渠道生成：邮箱保留首字符与完整域名；手机保留前 3 后 4
- 列表返回 `recipient_masked`
- **不存明文**，也不提供查看明文接口（需要时走单独的合规流程）
- 旧记录该字段为空，前端显示 `—`

迁移 `000033`：`notify_delivery` 增加 `recipient_masked text`。

### 2.3 失败原因中文化

维护错误码 → 中文原因映射（`kernel/notifier` 内）：

| code | 中文 |
|---|---|
| `message_unavailable` | 消息内容已过期或模板已删除 |
| `recipient_unavailable` | 收件人无效或已退订 |
| `provider_delivery_failed` | 服务商返回失败 |
| `channel_unavailable` | 没有可用的发送渠道 |
| `provider_timeout` | 服务商响应超时 |
| `config_invalid` | 渠道配置无效 |
| 其他 | 显示原码 |

响应同时给 `last_error`（原码）与 `last_error_message`（中文）。

### 2.4 重试语义

现有 `POST /deliveries/{id}/retry` 只允许 `failed` / `skipped`。

补充：

- `pending` 且 `attempts=0` 且创建超过 5 分钟 → 允许「重新入队」
  （对应 worker 没启动的场景；已入队事件重投是幂等的）
- `sent` 不允许重试（避免重复发送）
- 批量重试：`POST /deliveries/retry` body `{ids:[...]}`，返回 `{retried, skipped}`

### 2.5 列表筛选

`GET /deliveries` 增加：

| 参数 | 说明 |
|---|---|
| `status` | pending / sent / failed / skipped |
| `channel` | email / sms / wecom … |
| `template_code` | 精确筛选 |

## 3. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 模板名 | 列表返回 `template_name`，中文名正确；模板删除后回退 code |
| 2 | 收件人掩码 | 邮箱显示 `a***@163.com`；手机显示 `138****1234` |
| 3 | 不存明文 | 数据库 `notify_delivery` 中无完整邮箱/手机（检查列与内容） |
| 4 | 失败原因 | 失败记录返回中文 `last_error_message`，原始码仍在 |
| 5 | 未知码 | 未知错误码原样显示，不空白 |
| 6 | 重试失败项 | failed → 200，状态回 pending，worker 处理后可用 |
| 7 | 重试 pending | 卡住 5 分钟以上的 pending 可重新入队；5 分钟内返回 409 |
| 8 | 不可重试 | sent 重试返回 409 |
| 9 | 批量重试 | 3 条混合状态 → `{retried:2, skipped:1}` |
| 10 | 筛选 | status/channel/template_code 各自与组合生效 |
| 11 | 迁移 | 000033 up/down 通过；旧行 `recipient_masked` 为 null |
| 12 | 契约 | OpenAPI 更新，双向一致 |
| 13 | 全量 | `verify` 通过 |

## 4. 不做

- 查看收件人明文（合规流程另开）
- 重发已成功的投递
- 投递记录导出

## 5. Codex prompt

```
实现 docs/tasks/BE-IDN-015-delivery-readability.md。

分支：feat/be-idn-015-delivery-readability（从最新 main 开）

必读：本卡、internal/kernel/notifier/channel/http.go（deliveries 路由）、
internal/kernel/notifier/handler.go（写入投递记录处）、
internal/kernel/notifier/config.go、internal/bootstrap/notification_store.go、
notification_commands.go（Retry）、migrations/000014_notification*.up.sql

改动：
1. 迁移 000033：notify_delivery.recipient_masked
2. 写入时生成掩码（邮箱首字符+域名；手机前3后4）；不存明文
3. 列表返回 template_name、recipient_masked、last_error_message
4. 错误码中文映射（表见卡片 §2.3），未知码回退原码
5. retry 扩展：pending 超 5 分钟可重新入队；sent 拒绝；新增批量重试
6. 列表增加 status/channel/template_code 筛选
7. OpenAPI 同步

约束：
- 任何响应都不得出现完整邮箱或手机号
- 不改变已成功投递的记录
- worker 不在本卡范围

验收按卡片 §3 十三条逐条贴证据，第 3 条要贴数据库查询证明无明文。
完成后停下报告，不要做前端。
```

## 6. 实现记录与接口

- 分支 `feat/be-idn-015-delivery-readability`，基线 `73419a3`。
- 迁移 `000033_notification_recipient_masked` 只增加可空的 `text` 列，不回填历史记录；down 删除该列。
- 在入队与实际发送时生成掩码，只持久化哈希、域名和掩码。查不到收件人时仍保留原有可靠入队行为，由发送流程记录失败原因。
- 列表按租户关联模板的 `subject` 作为显示名，租户覆盖优先，同一范围优先 `zh-CN`，其余 locale 稳定排序；删除模板后回退 code，不因多个 locale 重复列表行。
- 错误码显示映射全部在 `kernel/notifier/channel` 内；原码不变，未知码回退原码。
- 重试只重新入队，交给已有发送流程处理；使用事务、行锁和活动租约检查。批量按 ID 排序锁定，成功记录不修改；支持原有 `Idempotency-Key` 重放。

| 方法 | 路径 | 权限 | 请求与响应 |
|---|---|---|---|
| GET | `/api/v1/admin/notifications/deliveries` | `system:notify:list` | 原分页参数及 `status` / `channel` / `template_code` 精确筛选；列表新增 `template_name`、可空 `recipient_masked`、`last_error_message` |
| POST | `/api/v1/admin/notifications/deliveries/{id}/retry` | `system:notify:manage` | failed / skipped，或创建严格超过 5 分钟、attempts=0 且无活动租约的 pending；200 返回 pending，sent 等不可重试状态返回 409 |
| POST | `/api/v1/admin/notifications/deliveries/retry` | `system:notify:manage` | `{ids:[...]}`，1–100 个不重复正整数；200 返回 `{retried,skipped}`；不存在、其他租户或不可重试的记录计入 skipped |

## 7. 十三项验收证据（2026-11-18）

专项命令（连接本机 PostgreSQL，使用独立临时测试库，不跳过集成测试）：

```powershell
go test -count=1 -v ./internal/kernel/notifier/channel ./internal/bootstrap -run '^(TestMaskRecipient|TestDeliveryErrorMessage|TestAdminNotificationReadability.*|TestAdminNotificationRetry.*|TestSystemNotificationDeliveryOpenAPIReadability|TestSystemOpenAPIRoutesAndPermissionsMatch)$'
```

```text
ok  github.com/omwk/go-admin-api/internal/kernel/notifier/channel  1.145s
ok  github.com/omwk/go-admin-api/internal/bootstrap                4.535s
```

| # | 证据 | 结果 |
|---|---|---|
| 1 | `TestAdminNotificationReadabilityIntegrationTemplateNamesAndFallback` | `系统通知` → 租户覆盖 `租户通知` → 删除后 `notification.default`；多 locale 时 total 仍为 1 |
| 2 | `TestMaskRecipient`、`TestAdminNotificationReadabilityIntegrationMasksAndNoPlaintext` | attempts=0 的入队记录即为 `a***@163.com` / `138****1234`；实际发送前地址变更后更新为新掩码 |
| 3 | 同上集成测试执行下方列清单与整行内容查询；发送后还检查 delivery/outbox/provider/audit/idempotency 等表 | 无明文收件人列，完整邮箱或手机号匹配行数 `0`；列表和重试响应也检查无完整收件人 |
| 4 | `TestDeliveryErrorMessage`、`TestAdminNotificationReadabilityIntegrationErrorsAndFilters` | 六个已知码全部映射；`message_unavailable` 原码保留，中文为 `消息内容已过期或模板已删除` |
| 5 | 同上 | `future_error` 的 last_error 与 last_error_message 均为 `future_error` |
| 6 | `TestAdminNotificationRetryIntegrationFailedQueuesThenDelivers` | failed → HTTP 200 pending → 已有 dispatcher 实际发送成功；相同幂等键重放后 sent 行完整快照不变 |
| 7 | `TestAdminNotificationRetryIntegrationPendingBoundaryAndSent` | 4 分钟 / 恰好 5 分钟为 409，5 分 1 秒为 200；attempts>0 / 活动租约为 409，过期租约可重新入队 |
| 8 | 同上 | sent 返回 409；重试前后数据库整行 JSON 相同 |
| 9 | `TestAdminNotificationRetryIntegrationMixedBatchAndTenantIsolation` | failed + 超时 pending + sent → `{retried:2,skipped:1}`；随后发送两次，原 sent 行不变；幂等重放计数相同 |
| 10 | `TestAdminNotificationReadabilityIntegrationErrorsAndFilters` | status=failed 共 3，channel=email 共 2，template_code=notification.default 共 3，三者组合共 1；无匹配为 0，分页 total 不变，其他租户排除 |
| 11 | `TestAdminNotificationReadabilityMigrationIntegrationThirtyThree` | 真实 `database.Migrate(up/down/up)`：32 → 33 → 32 → 33；旧行掩码为 NULL，sent/attempts 保持不变，down 后列不存在 |
| 12 | `TestSystemNotificationDeliveryOpenAPIReadability`、`TestSystemOpenAPIRoutesAndPermissionsMatch` | actual=51 / manifest=51 / OpenAPI=51；响应字段双向一致、可空掩码、五个列表参数、批量 ID 边界和权限一致 |
| 13 | `powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1 -Integration` | go vet、lint、全部测试与集成测试通过；最后一行 `all checks passed` |

第 3 项实际数据库查询（参数绑定本次生成的完整收件人，日志不打印参数值）：

```sql
SELECT column_name, data_type
FROM information_schema.columns
WHERE table_schema = current_schema() AND table_name = 'notify_delivery'
ORDER BY ordinal_position;
-- id bigint; tenant_id bigint; channel text; template_code text;
-- recipient_hash text; recipient_domain text; provider_id bigint; status text;
-- attempts integer; last_error text; created_at timestamptz; sent_at timestamptz;
-- next_retry_at timestamptz; recipient_masked text

SELECT count(*) AS plaintext_rows
FROM notify_delivery d
WHERE strpos(row_to_json(d)::text, $1) > 0
   OR strpos(row_to_json(d)::text, $2) > 0;
-- plaintext_rows = 0
```

完整专项输出保存在本机 `%TEMP%/be-idn-015-evidence-final.log`，全量输出为 `%TEMP%/be-idn-015-verify.log`。

全量集成测试汇总：

```text
ok  github.com/omwk/go-admin-api/internal/kernel/db   11.992s
ok  github.com/omwk/go-admin-api/internal/bootstrap  386.225s
ok  github.com/omwk/go-admin-api/cmd/cli               32.148s
all checks passed
```

## 8. 自检与清理

- `make lint`：退出码 0，最后一行 `golangci-lint run`。
- `make test`：退出码 0，45 个测试包通过、0 失败，最后一个测试包汇总为 `ok github.com/omwk/go-admin-api/scripts/smoke (cached)`；verify 中的全量 `go test ./...` 同样通过。未删除或放宽既有行为断言，原同步重试测试改为断言入队后由真实 dispatcher 发送。
- 迁移 up/down：上述 `TestAdminNotificationReadabilityMigrationIntegrationThirtyThree` 调用实际迁移执行器；全量 verify 还运行 `internal/kernel/db` 的全链 up/down 到底再 up、并发启动和结构不变量测试。
- `powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-test-groups.ps1`：`Source 589 = shards 588 + excluded 1`，每个集成测试恰好归属一个分片或显式排除；本卡新增测试沿用 Admin 前缀进入 core。
- `rg -n 'TODO|not implemented' internal`：0 匹配；新运行路径无日志输出、调试打印或敏感错误插值；隐私集成测试同时检查响应与持久化内容。
- 对全部 17 个变更 Go 文件用 `go/parser` / `go/ast` 检查：最大文件 252 行（channel/service.go），最大函数 56 行（dispatch）；`git diff --check` 通过。
- 缓存沿用仓库外默认位置，未新增依赖，未修改前端、worker 或历史迁移。
- 已清理：测试框架逐库执行 `DROP DATABASE`；查询 `SELECT count(*) FROM pg_database WHERE datname=ANY($1)` → checked=16，remaining=0（两轮专项验收）。本卡数据、模板和审计全部随隔离库删除；未修改开发库数据或系统配置，无保留测试业务数据。

## 9. 偏差说明与遗留

- 业务实现无偏差。基线不存在提示中的 `kernel/notifier/handler.go`、顶层 `config.go` 和 `000014_notification*.up.sql`；实际投递写入位于 `kernel/notifier/channel/service.go`，配置校验位于同目录 `config.go`，三张通知表由 `000023_notification_channels.up.sql` 定义。按实际代码阅读和实现，未修改历史迁移。
- `notify_template` 既有显示标题字段为 `subject`，因此关联该字段返回 template_name，未额外增加名称列。
- 本地未运行 race，按 `AGENTS.md` §11 留待合并前 CI 验证。未推送、未创建或合并 PR，完成后停在本卡报告。
- 遗留业务工作：无。
