# BE-IDN-017 · 模板显示名

- ID: BE-IDN-017
- 类型: 修复
- 仓库: 后端
- 状态: 已完成
- 预计: 0.5 人日
- 分支: `feat/be-idn-017-template-name`
- 依赖: BE-IDN-015（投递列表）、BE-IDN-016（模板重写）

---

## 1. 现象

BE-IDN-016 把模板 subject 改成了带变量的文案后，投递记录页的「模板」列变成：

```
验证你的 {{.AppName}} 邮箱
重置你的 {{.AppName}} 密码
```

根因：BE-IDN-015 的 `template_name` 直接取 `subject`：

```sql
coalesce(nullif(t.subject,''), d.template_code) AS template_name
```

subject 现在含占位符，未渲染时不可读。

## 2. 改动

### 2.1 模板表增加显示名

迁移 `000034`：`notify_template` 增加 `name text NOT NULL DEFAULT ''`。

内置模板种子填入：

| code | name |
|---|---|
| `identity.verify` | 验证注册邮箱 |
| `identity.reset` | 重置密码 |
| `identity.invite` | 账号邀请 |
| `notification.test` | 通道测试 |
| `notification.default` | 系统通知 |
| `credential.expiring.3d` | 凭据即将过期（3 天） |
| `credential.expiring.1d` | 凭据即将过期（1 天） |
| `credential.expired` | 凭据已过期 |
| `credential.needs_reauth` | 凭据需要重新授权 |
| `credential.failed` | 凭据校验失败 |
| `credential.stuck` | 凭据同步卡住 |
| `credential.recovered` | 凭据已恢复 |
| `credential.unknown` | 凭据状态未知 |
| `credential.protocol_pending` | 凭据协议待确认 |

### 2.2 投递列表取值顺序

```sql
coalesce(nullif(t.name,''), nullif(t.subject,''), d.template_code) AS template_name
```

- 有 name 用 name
- 旧数据/自定义模板无 name → subject（可能含占位符，属自定义内容）
- 都没有 → code

### 2.3 模板管理接口

`POST/PUT /admin/notifications/templates` 接受 `name`（≤128，可空）。
模板列表返回 `name`。

## 3. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 投递列表 | 「模板」列显示「验证注册邮箱」而不是含占位符的 subject |
| 2 | 兼容 | 无 name 的自定义模板回退 subject，再回退 code |
| 3 | 接口 | 模板 CRUD 支持 name，列表返回 |
| 4 | 迁移 | 000034 up/down 通过；存量行 name 为空 |
| 5 | 种子 | 重跑 seed 后内置模板 name 齐全 |
| 6 | 契约 | OpenAPI 模板对象增加 name |
| 7 | 全量 | `verify` 通过 |

## 4. 不做

- 模板多语言名称
- 投递记录页（前端无需改动，字段名不变）

## 5. Codex prompt

```
实现 docs/tasks/BE-IDN-017-template-name.md。

分支：feat/be-idn-017-template-name（从最新 main 7f3f3c1 开）

必读：本卡、internal/bootstrap/notification_deliveries.go（template_name SQL）、
internal/bootstrap/notification_management.go（模板 CRUD）、
模板种子所在文件、migrations/ 模板表定义

改动：
1. 迁移 000034：notify_template.name
2. 种子填 14 条内置模板 name（见卡片 §2.1）
3. 投递列表 template_name 取值改为 name → subject → code
4. 模板 CRUD 支持 name，列表返回
5. OpenAPI 同步

约束：
- 不改 subject/body 语义
- 不改投递列表字段名

验收按卡片 §3 七条逐条贴证据。
完成后停下报告，不要做前端。
```

## 6. 实现与验收记录（2026-11-19）

基点：`7f3f3c1`；分支：`feat/be-idn-017-template-name`。
新增迁移 `000034_notification_template_name`，模板模型、查询投影、种子、管理接口及 OpenAPI 同步增加 `name`。
`subject` / `body` 的内容和渲染语义保持不变，投递列表继续返回 `template_name`。

### 6.1 接口记录

| 方法 | 路径 | 本卡变更 |
|---|---|---|
| GET | `/api/v1/admin/notifications/templates` | 模板对象总是返回 `name`，没有显示名时为 `""` |
| PUT | `/api/v1/admin/notifications/templates/{code}` | 沿用现有按 code/locale 创建或更新模板的接口；接受可省略、可清空的 `name`，最多 128 个 Unicode 字符；超长返回 400/10001 |
| GET | `/api/v1/admin/notifications/deliveries` | `template_name` 按选中模板的 `name` → `subject` → `template_code` 回退；租户与 locale 的原有优先级不变 |

### 6.2 §3 七项证据

定向命令与完整输出：[acceptance.txt](evidence/BE-IDN-017/acceptance.txt)。

| # | 项 | 验收结果与证据 |
|---|---|---|
| 1 | 投递列表 | `TestAdminNotificationTemplateNameIntegrationDeliveries` PASS：`identity.verify -> 验证注册邮箱`，subject 保留 `{{.AppName}}` 模板内容 |
| 2 | 兼容 | 同一用例 PASS：非空 name 优先；空 name → `旧标题 {{.Name}}`；subject 也为空或模板已删除 → code；原有租户、多 locale 回归用例通过 |
| 3 | 接口 | `TestAdminNotificationTemplateNameIntegrationCRUD` PASS：创建、修改、清空、省略 name 均通过 PUT/GET 往返验证；128 个中文字符成功；129 个中文或 ASCII 字符返回 400/10001；subject/body 不变 |
| 4 | 迁移 | `TestAdminNotificationTemplateNameMigrationIntegrationThirtyFour` PASS：`database.Migrate(up/down/up)` 执行 `33 -> 34 -> 33 -> 34`；列为 `text NOT NULL DEFAULT ''`；存量行 name 为空；down 删除列，subject/body 保留 |
| 5 | 种子 | `TestSeededTemplateNames`、`TestSeedAccountTemplatesIntegrationRefreshBuiltinsAndPreserveCustom` PASS：按 §2.1 校验全部 14 个名称；真实 `SeedIdentity` 连跑两次后全局与租户内置模板名称齐全；启停标志和自定义模板保留 |
| 6 | 契约 | `TestSystemNotificationTemplateNameOpenAPI` PASS：`NotificationTemplate`、`NotificationTemplateRequest` 均声明 `name: string, maxLength: 128`；请求可省略，响应必定返回 |
| 7 | 全量 | `powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1 -Integration` 退出 0，最后输出 `all checks passed`；包含 vet、lint、全量测试及无缓存 DB/bootstrap/CLI 集成测试，见 [verify.txt](evidence/BE-IDN-017/verify.txt) |

### 6.3 自检与清理

- `make lint`：`make lint: exit 0`，见 [make-lint.txt](evidence/BE-IDN-017/make-lint.txt)。
- `make test`：`make test: exit 0`，见 [make-test.txt](evidence/BE-IDN-017/make-test.txt)。
- `verify -Integration`：`all checks passed`；无缓存集成测试 DB `14.200s`、bootstrap `573.012s`、CLI `57.059s`，见 [verify.txt](evidence/BE-IDN-017/verify.txt)。
- 全链 up、逐步 down 到底再 up、并发迁移启动及关键结构不变量随完整 DB 集成测试执行。
- `rg -n 'TODO|not implemented' internal/`：0 命中；审查新增 diff，没有新增日志、敏感信息、调试输出或依赖，见 [self-check.txt](evidence/BE-IDN-017/self-check.txt)。
- Go AST 检查：`go run "$env:TEMP/be-idn-017-evidence/check-go-size.go" -- <本卡改动的 Go 文件>`；最大文件 265 行、最大函数 63 行，见 [go-size.txt](evidence/BE-IDN-017/go-size.txt)。
- 测试分组守卫：`Source 594 = shards 593 + excluded 1`；本卡三个新增集成测试均仅由 core 分片执行。
- 已清理：验收通过 `internal/testenv` 创建隔离数据库，测试结束由 `t.Cleanup` 删除；没有在开发库创建本卡测试数据、审计或临时系统配置。

### 6.4 偏差说明

- 功能偏差：无。
- 按 AGENTS.md 的迁移测试维护例外，000033 的已有测试在全链 up 后显式回到 33 再断言；原有列、默认值、状态和回滚断言全部保留。
- 未改前端。
