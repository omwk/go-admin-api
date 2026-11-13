# S2 · 系统配置与数据字典（后端）

- 类型：系统层补全（`docs/design/system-layer-plan.md` S2）
- 预计：**3 人日**
- 分支：`feat/s2-config-dict`
- 依赖：无

---

## 1. 现状

| 能力 | 现状 |
|---|---|
| 系统配置 | `sys_setting` 表存在（`group_key` / `item_key` / `value jsonb`），**无任何接口**，值靠代码硬编码 |
| 数据字典 | **无表**，前端下拉与状态文案全部写死 |
| 错误码字典 | 常量散落，前端无法做统一文案映射 |

## 2. 系统配置

### 2.1 接口

```
GET /admin/settings              全部分组（含 schema 与当前值）
GET /admin/settings/{group}      单个分组
PUT /admin/settings/{group}      批量更新该组
```

权限：`system:setting:list` / `system:setting:update`。

### 2.2 注册表（关键设计）

配置项**不能是任意键值对**——没有 schema 的配置中心等于给生产库装了个随意改代码的入口。

在代码里维护一份注册表：

```go
type Item struct {
    Group    string   // security / notification / system
    Key      string   // password_min_length
    Type     string   // string | number | boolean | json
    Default  any
    Min, Max *float64 // number 类型
    Enum     []string // 可选值
    ReadOnly bool     // 展示但不可改
    Desc     string
}
```

首批条目（三组）：

| group | key | type | 说明 |
|---|---|---|---|
| security | `password_min_length` | number | 8–64，默认 8 |
| security | `password_require_mixed` | boolean | 默认 true |
| security | `password_expire_days` | number | 0–365，0 表示不过期，默认 90 |
| security | `password_history_size` | number | 0–20，默认 5 |
| security | `login_max_failures` | number | 3–20，默认 5 |
| security | `login_lock_minutes` | number | 1–1440，默认 15 |
| system | `system_name` | string | 1–64，默认 CommerceHub |
| system | `default_page_size` | number | 10–100，默认 20 |
| system | `timezone` | string | IANA 名称，默认 Asia/Shanghai |
| notification | `default_channel` | string | 枚举 email/sms/wecom/dingtalk/webhook |
| notification | `retry_max_attempts` | number | 0–10，默认 3 |

### 2.3 规则

- 未注册的 key → 400（**不能**接受任意键）
- 值按类型与范围校验，失败 400 并指明哪个 key
- `read_only` 项 PUT 忽略并返回 400
- 每次 PUT 记审计（before/after 摘要，不含敏感值）
- 租户可覆盖全局值：先查租户行，没有再落默认（本次只做租户级，全局默认在代码里）
- 配置值变更**不热更新运行中的应用**，除显式声明支持的项（本批：`password_*`、`login_*`、`default_page_size`）

> 热更新范围必须写清楚，否则「改了没生效」会成为反复出现的问题。

## 3. 数据字典

### 3.1 表

```
sys_dict        (id, tenant_id, type, name, status, created_at, updated_at)
sys_dict_item   (id, tenant_id, dict_id, label, value, sort, status, color, extra jsonb)
唯一：(tenant_id, type)、(tenant_id, dict_id, value)
```

### 3.2 接口

```
GET    /admin/dicts               类型列表
POST   /admin/dicts
PUT    /admin/dicts/{id}
DELETE /admin/dicts/{id}
GET    /admin/dicts/{id}/items
POST   /admin/dicts/{id}/items
PUT    /admin/dict-items/{id}
DELETE /admin/dict-items/{id}
GET    /meta/dicts/{type}         按类型取值（登录用户可读，供前端下拉）
```

权限：`system:dict:list` / `system:dict:create` / `system:dict:update` / `system:dict:delete`。

### 3.3 规则

- 删除字典前必须为空（有项 → 409）
- `value` 在字典内唯一
- 内置字典（如 `order_status`）标记 `builtin=true`，禁止删除但允许改 label 与排序
- `GET /meta/dicts/{type}` 返回启用项，按 sort 升序，带 60 秒缓存

### 3.4 种子

至少内置三份，供前端立即使用：

```
order_status     pending_payment / paid / shipped / completed / cancelled / refunded
user_status      active / disabled / locked
sync_run_status  pending / running / succeeded / failed
```

## 4. 错误码字典

```
GET /meta/error-codes
```

返回：

```json
[{ "code": 10001, "message": "参数错误", "http": 400, "domain": "common" }, ...]
```

来源：在 `internal/kernel/errs` 增加一张**可枚举的注册表**（现有常量改为从注册表引用，值不变），
避免手工维护两份。

前端用它做文案映射，缺失的码回退到后端 message。

## 5. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 配置读取 | `GET /admin/settings` 返回三组、11 项，含 schema 与默认值 |
| 2 | 配置写入 | PUT 合法值成功，`GET` 回显一致 |
| 3 | 类型校验 | `password_min_length="abc"` → 400 且指明 key |
| 4 | 范围校验 | `password_min_length=3` → 400；`=8` → 200 |
| 5 | 未注册键 | PUT 未注册 key → 400 |
| 6 | 只读项 | PUT `read_only` 项 → 400 |
| 7 | 审计 | 每次 PUT 产生一条审计，含 key 与前后值摘要 |
| 8 | 字典 CRUD | 建类型、建项、改、删（先清空）全通过 |
| 9 | 字典唯一 | 同类型重复 value → 409 |
| 10 | 内置字典 | 删除内置字典 → 409；改 label 成功 |
| 11 | 前端字典 | `GET /meta/dicts/order_status` 返回 6 项、按 sort 排序 |
| 12 | 错误码 | `GET /meta/error-codes` 覆盖现有全部常量，code 与常量逐字一致 |
| 13 | 越权 | 跨租户读写 → 404 |
| 14 | 契约 | 新增接口全部进 OpenAPI |
| 15 | 全量 | `scripts/verify.ps1 -Integration -Migration` 通过 |

## 6. 不做

- 配置项热更新到所有运行节点（本批只支持列出的项）
- 全局配置（只做租户级）
- 字典项国际化
- 前端页面（单独一张卡）

## 7. Codex prompt

```
实现 docs/tasks/S2-config-dict.md。

必读：本卡、docs/design/system-layer-plan.md S2、
internal/kernel/errs/（错误码常量）、internal/modules/identity/（权限与审计写法）、
api/openapi/identity.yaml

范围：
1. 系统配置：注册表 + GET/PUT（三组 11 项），类型/范围/只读/未注册校验，审计
2. 数据字典：两张新表 + CRUD + /meta/dicts/{type}，内置三份字典种子
3. 错误码字典：errs 增加可枚举注册表（值不变），GET /meta/error-codes
4. 全部进 OpenAPI；权限码按卡片

约束：
- 配置项必须走注册表，禁止任意键
- 字典内置项禁止删除
- 审计不记录敏感值
- 新增迁移号从 000025 起

验收按卡片 §5 十五条逐条贴证据。
完成后停下报告，不要做前端。
```

## 8. 实施与验证计划（2026-11-13）

本卡即实施计划与验收记录，避免在任务范围外增加计划文件。仅使用现有依赖。

- [x] 配置：新增 `internal/kernel/setting/` 注册表及租户读取端口；新增 `internal/modules/system/` 配置服务和 HTTP 接口；在 `internal/bootstrap/system_setting*.go` 实现带租户条件的读写。GET 返回 `[{group, items:[{schema,value}]}]`，单组 GET/PUT 返回一个组；PUT 请求体直接为该组的键值对象。先执行类型、范围、未注册键和只读测试，再实现与验证。
- [x] 字典：新增 `internal/modules/system/dict*.go` 与 `internal/bootstrap/system_dict*.go`，实现类型和项 CRUD、内置保护、60 秒租户缓存；`000025` 建表、`000026` 初始化已有租户，seed 为以后初始化的租户补种。管理列表使用标准分页；meta 返回启用的 `{label,value,sort,color,extra}` 数组。先补接口行为测试，再验证事务、唯一约束和隔离。
- [x] 生效项：identity 通过 `setting.Reader.Values(ctx, tenant, group)` 获取当前策略；`setting.LoadSecurity(ctx, reader, tenant)` 与 `setting.DefaultPageSize(ctx, reader, tenant)` 负责默认值。覆盖密码长度/混合/期限/历史、登录失败阈值/锁定时长和默认分页，其他项仅保存与展示。
- [x] 错误码与契约：现有 errs 导出编号从唯一注册表获取，数值保持不变；`api/openapi/system.yaml` 记录本卡全部 13 个操作及校验/权限/响应，新增契约一致性测试。
- [x] 装配与验收：在 bootstrap 注册路由、权限和种子；以 `TestSystem...Integration...` 命名新增集成测试并纳入 core 分片。执行局部测试、`make lint`/`make test`（工具缺失时记录等价命令）、分片守卫、`scripts/verify.ps1 -Integration -Migration`，逐条记录 §5 的 15 项证据。

协作边界：配置/装配由主实现负责；字典、策略接入、错误码与 OpenAPI 分别限定独立文件。审计与业务写入同事务，配置仅记录 key、类型和前后摘要；内置字典与内置项禁止删除。

## 9. 验收证据（2026-11-13）

专项命令：`go test -count=1 -v ./internal/bootstrap -run '^(TestSystem|TestSeedIntegrationDepartmentUpgrade)'`，输出 `PASS`、`ok github.com/omwk/go-admin-api/internal/bootstrap 6.868s`。下表中的 HTTP 请求均经现有 OpenAPI 响应校验器验证。

| # | 验收结果与证据 |
|---|---|
| 1 | `TestSystemSettingsIntegrationAcceptance`：GET 返回 3 组、11 项，每项包含 schema，初始值等于注册默认值。 |
| 2 | 同上：PUT 最小密码长度 12 返回 200，GET 仍为 12；同一幂等键重放结果相同，换请求体返回 409。 |
| 3 | 同上：`password_min_length="abc"` 返回 400，`details.key=password_min_length`。 |
| 4 | 同上：值 3 / 8.5 返回 400，8 返回 200；混合有效/无效字段的整批写入不产生部分更新。 |
| 5 | 同上：未注册 `unknown` 返回 400，并指明 key。 |
| 6 | `TestSystemSettingsIntegrationReadOnlyAndAuditRollback`：在同一份 11 项 schema 中将 `system_name` 标为只读，PUT 返回 400，原值保持 CommerceHub；首批生产配置不额外添加第 12 项。 |
| 7 | `TestSystemSettingsIntegrationSafeAuditAndLivePageSize`：成功 PUT 产生一条审计，before/after 包含 key、类型和 digest，不含运行时生成的字符串原值；前一测试另验证审计失败时配置事务回滚。 |
| 8 | `TestSystemDictIntegrationCRUDAndUniqueness`：类型/项创建 201、修改 200、删除 204；未清空字典返回 409，清空后可删，再删返回 404。 |
| 9 | 同上：重复类型和字典内重复 value 均返回 409。 |
| 10 | `TestSystemDictIntegrationBuiltinMetadataAndSeeds`：内置类型及项删除均返回 409，label/sort 修改成功，内置 value 修改返回 409；重复 seed 保留自定义 label。 |
| 11 | 同上：`order_status` 六项值逐一匹配种子顺序，调整 sort 后立即反映，禁用项后返回五项；另外两份种子分别为 3 / 4 项。`TestDictionaryMetaCacheExpiresAtSixtySeconds` 验证第 59 秒命中、第 60 秒过期、租户隔离和写后失效。 |
| 12 | `TestSystemMetadataIntegrationErrorCodesAndPermissions`：HTTP 13 项逐项等于 `errs.All()`；`TestRegisteredCodesPreserveAllOriginalValues` 将每个旧编号与导出标识逐一比较，并验证枚举无重复、升序和副本所有权。 |
| 13 | `TestSystemDictIntegrationTenantIsolation`：跨租户的七种类型/项读写请求全部 404，列表不泄露；`TestSystemSettingsIntegrationTenantOverrides` 验证租户 1 写入不改变租户 2 的覆盖值，客户端不能注入 tenant_id。 |
| 14 | `TestSystemOpenAPIRoutesAndPermissionsMatch`：实际路由、权限注册表与 `api/openapi/system.yaml` 双向比对，全部 13 个操作一致；普通已登录用户可访问 meta，管理权限缺失返回 403，匿名 meta 返回 401。 |
| 15 | `powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1 -Integration -Migration` 退出码 0，末行 `all checks passed`；全量 bootstrap 294.786s，强制重跑集成 bootstrap 283.947s，迁移版本 26 → 25 → 26，均 `dirty=false`。 |

附加行为：`TestSystemSettingsIntegrationLivePasswordAndLoginPolicy`、`TestSystemSettingsIntegrationLivePasswordHistory`、`TestSystemSettingsIntegrationLivePasswordExpiry` 验证密码长度/混合、历史容量、期限、失败锁定阈值与时长随配置生效；默认分页变更同时在字典列表与 identity 用户列表验证。

## 10. 自检、命令与偏差说明

- [x] 本机无 `make`；`verify.ps1` 执行等价的 `golangci-lint run`、`go test ./...`，并额外执行 `go vet ./...`，全部退出码 0。
- [x] 全量集成与 CLI 迁移通过。迁移只使用本次创建的独立验证数据库，完成后已删除；原应用库不执行 down。
- [x] 所有新增接口已在本卡 §2–4 和 `api/openapi/system.yaml` 记录，并完成双向契约校验。
- [x] `rg -n 'TODO|not implemented' internal/` 无匹配；`git diff --check` 通过。
- [x] 检查新增 system/setting 包无直接日志调用；配置审计仅写摘要，字典 extra 仅记长度；集成测试检查审计不含运行时字符串原值，审计失败则事务回滚。
- [x] 对全部变更 Go 文件执行 `gofmt -l` 与 Go AST 尺寸检查：50 个文件，最大 306 行，最大函数 80 行；检查器放在系统临时目录，无第三方依赖。
- [x] `powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-test-groups.ps1`：`Source 439 = shards 438 + excluded 1`，每个集成测试恰好归属一个分片或既有显式排除项。
- [x] `GOCACHE` / `GOLANGCI_LINT_CACHE` 使用系统临时目录，未指向仓库。

完整验证输出摘要：

```text
$ powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1 -Integration -Migration
== go vet ==
== golangci-lint ==
== go test ==
ok github.com/omwk/go-admin-api/internal/bootstrap 294.786s
ok github.com/omwk/go-admin-api/internal/modules/identity 18.644s
== integration ==
ok github.com/omwk/go-admin-api/internal/kernel/db 6.919s
ok github.com/omwk/go-admin-api/internal/bootstrap 283.947s
ok github.com/omwk/go-admin-api/cmd/cli 12.142s
== migrate up/down/up ==
version=26 dirty=false
version=25 dirty=false
version=26 dirty=false
all checks passed
Isolated migration database removed
```

CLI 实际迁移命令依次为 `go run ./cmd/cli migrate up`、`go run ./cmd/cli migrate down`、`go run ./cmd/cli migrate up`。完整日志在本机临时目录 `codex-s2-verify-final.log`；分片日志为 `codex-s2-test-groups.log`。

尺寸检查命令（检查器基于标准库 `go/parser` / `go/ast`）：

```powershell
$s2ChangedGo = @(git -c core.safecrlf=false diff --name-only -- '*.go') + @(git ls-files --others --exclude-standard -- '*.go')
go run "$env:TEMP/codex-s2-shape-f630499b6a2940f2b597ae6c24ad3f15.go" -- $s2ChangedGo
```

偏差说明：功能范围无偏差；首批通知渠道未规定默认值，采用已注册枚举中的 email 并记录在 schema。现有权限投影守卫按六个新增权限逐项注明前端页另卡实现；种子升级测试改用注册目录总数，并保持原有增量、幂等与授权断言，补充逐权限存在性校验。本机 `CGO_ENABLED=0`，race 尚需 CI 验证。
