# BE-IDN-008 · 字典供给修复（按审查报告修订）

- ID: BE-IDN-008
- 类型: 修复
- 仓库: 后端
- 状态: 后端已实现并通过全量验证；前端自举与前端报告状态待后续任务
- 预计: 1.2 人日
- 分支: `feat/be-idn-008-dict-supply`
- 依赖: BE-7（字典分域，已合并）
- 来源：`docs/reports/system-dictionary-2026-11-15/report.md`（前端仓库）
  + `docs/design/dictionary-usage.md`

### 后端执行计划（2026-11-15）

- [x] 在 `internal/bootstrap/system_dict_seed*.go` 集中声明 9 个系统字典（保留 user_status），每项给出 label/value/sort/color；默认 seed 与租户物化共用 `seedDictsForTenant(ctx, tenantID)`，只补不覆盖。
- [x] 在 `internal/bootstrap/system_dict_maintenance.go` 与 `cmd/cli/dict.go` 增加全租户 `dict-backfill` 和非生产 `dict-reset`；单租户事务中加既有字典锁，删除项后删除类型再重种，输出各租户前后计数。
- [x] 在 `internal/modules/system/dict_fields.go` 集中声明允许更新字段，以完整修改前后对象校验拒绝未登记字段；保持现有 PUT 和错误码契约。
- [x] 增加种子/Provision/CLI/白名单回归测试，验证真实 CHECK 与订单 SQL 投影、业务文案保留、幂等及生产拒绝；运行 verify、相关集成测试和 CI 分组守卫。
- [x] 更新本卡和 `docs/design/dictionary-usage.md`，逐条记录十二项验收；第 9 条前端本地兜底和前端审查报告状态按本次要求只读核查，不改前端。

---

## 1. 为什么要改（原卡片判断有误）

初版卡片只打算「补几个字典类型」。核对 Codex 的审查报告与实测后，发现三处**供给缺陷**：

### P0-1 · 新租户完全没有字典

```
tenant 1  → 3 个类型
tenant 2–14 → 0 个
```

`000026` 迁移只给「迁移当时已存在」的租户插了数据；
`identity_tenant_materialize.go` 的物化流程**不含字典**。

结果：任何新开通租户的 `/meta/dicts/*` 全空，所有字典驱动的 UI 回退原码。

### P0-2 · `sync_run_status` 值域与实际不符

| 字典现有 | 真实 `sync_run.status`（迁移 CHECK） |
|---|---|
| pending / running / succeeded / failed | running / success / partial / failed / cancelled |

四个值只有两个对得上，这个字典**从未生效过**。

### P0-3 · `order_status` 与订单展示态不匹配

| 字典现有 | 接口展示投影（`orderDisplaySQL`） |
|---|---|
| pending_payment / paid / shipped / completed / cancelled / refunded | pending_payment / pending_shipment / shipped / completed / closed / refunding / refunded / unknown |

## 2. 改动

### 2.1 新增/修正字典类型

| type | scope | 值 | 说明 |
|---|---|---|---|
| `product_status` | system | draft / active / archived | 前端已在调用，供给缺失 |
| `sync_run_status` | system | running / success / partial / failed / cancelled | 改为与真实 CHECK 一致（原值作废） |
| `order_status` | system | 8 个展示态 | 改为与 `orderDisplaySQL` 一致（原值作废） |
| `tenant_status` | system | active / suspended | 租户页直接显示原码 |
| `audit_result` | system | success / failure | 审计与登录历史 |
| `enable_status` | system | active / disabled | 部门/角色/菜单/渠道启用态 |
| `delivery_status` | system | pending / sent / failed / skipped | 投递记录 |
| `invite_status` | system | active / expired / revoked / used | 邀请列表 |

**旧类型处理（2026-11-15 决定）**：当前所有租户均为开发阶段数据，**不做兼容**。

- `sync_run_status` 直接改为真实值域，不建 v2
- `order_status` 直接改为订单展示态的 8 个值，不建 order_display_status
- 多余的历史 item 直接删除

实现方式：新增开发用 CLI `dict-reset`（`app.env=prod` 拒绝执行），
清空字典与项后按代码重新种入。`seed` 本身仍保持「只补不覆盖」的语义，
生产升级不受影响。

### 2.2 字典供给覆盖所有租户（P0）

新增 `seedDictsForTenant(ctx, tenantID)`：

- 由 `SeedIdentity`（默认租户）调用
- 由租户物化流程（`Provision` / `Materialize`）调用
- 幂等（`ON CONFLICT` 按 type/scope）
- **只补缺失，不覆盖已有 label/sort/color**

再补一个一次性回填：为**已存在但无字典的租户**补种（迁移或 CLI），
验收要求租户 2–14 都从 0 变成完整集合。

### 2.3 字典自举保护

字典管理页的「状态」下拉当前依赖 `user_status` 字典。若该字典被停用，
维护页自身不可用。

处理：`user_status` 标记为 `builtin` 且**禁止停用/删除**（不只是禁增删项），
或在字典页对状态列使用本地固定两值（active/disabled）兜底。
本卡采用后者 + 文档说明，避免字典页依赖任何字典。


### 2.4 scope 口径（2026-11-15 复核后统一）

**business 与 system 的区别不是「能不能改文案」，而是「码值能否增删」**：

| | system | business |
|---|---|---|
| 码值（value）增删 | 禁止 | 允许（后续接入引用保护） |
| value / type 修改 | 禁止 | 允许（未引用时） |
| 字典 name / status | 允许 | 允许 |
| 项 label / sort / color / status | 允许 | 允许 |

因此本卡 8 个类型**全部为 system**：商品状态、订单展示态、同步状态、租户状态、
审计结果、启用状态、投递状态、邀请状态都是固定集合，字典只承载展示属性。

usiness 保留给真正可扩展的分类（如 customer_source、customer_level，
待盘点数据后另卡接入）。

### 2.5 服务层可写字段白名单（补强）

现状：后端已拒绝「改 type」「改 value」「系统字典加/删项」，
但**允许字段没有集中声明**，靠各处 if 判断。补一个显式白名单：

`go
// system 字典可写：name、status
// system 项可写：label、sort、color、status、extra
// 其余字段（type/value/scope/builtin）一律拒绝
`

要求：白名单集中在服务层，配单测覆盖每个被拒字段，避免将来新增字段默认放行。
## 3. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 类型齐全 | `/admin/dicts` 含上表 8 个类型，项数与值域一致 |
| 2 | 新租户 | 新开通租户自动拥有全部字典（用 Provision 后查询验证） |
| 3 | 存量租户 | 租户 2–14 回填后字典数与默认租户一致 |
| 4 | 值域一致 | `sync_run_status` 与迁移 CHECK 逐字一致；`order_status` 与 `orderDisplaySQL` 逐字一致 |
| 5 | 旧值作废 | `dict-reset` 后旧值（succeeded/paid/cancelled）不再存在 |
| 6 | 增量不覆盖 | 改某业务字典 label 后重跑 seed 与回填，label 保持 |
| 7 | 前端取值 | `/meta/dicts/product_status` 返回 3 项含 color |
| 8 | 可写白名单 | system：改 name/status/label/color/sort 允许；改 type/value、加项、删项、删字典均拒绝 |
| 9 | 自举 | 停用 `user_status` 后字典页仍可打开与维护（走本地兜底） |
| 10 | 幂等 | 连续两次 seed + 回填，项数不变无重复 |
| 11 | 契约 | 无接口变化；`dictionary-usage.md` 与审查报告状态更新 |
| 12 | 全量 | `verify` 通过 |

## 4. 不做

- 不迁移 81 组展示候选中剩余的部分（按审查报告分批）
- 不做任何旧值兼容（当前均为开发数据）
- 不改任何业务状态机与 CHECK 约束
- 不动 `customer_source` / `customer_level`（需先盘点数据，见报告 P2/P3）

## 5. Codex prompt

```
实现 docs/tasks/BE-IDN-008-dict-supply.md。

分支：feat/be-idn-008-dict-supply（从最新 main 开）

必读：本卡、docs/reports/system-dictionary-2026-11-15/report.md §2、
docs/design/dictionary-usage.md、
internal/bootstrap/system_dict_seed.go、identity_seed.go、
identity_tenant_materialize.go、migrations/000026、000028、
internal/modules/system/dict_write.go

改动：
1. 修正/新增 8 个字典类型（见 §2.1），旧值直接作废
2. seedDictsForTenant：默认租户 seed 与租户物化都调用，幂等、只补不覆盖
3. 新增 cmd/cli dict-reset：清空与重种（prod 拒绝执行）
4. 用 dict-reset 或回填覆盖全部存量租户
5. 字典页状态列改用本地兜底两值，消除自举依赖

约束：
- 不保留旧值；但不改业务状态机与 DB CHECK
- 不覆盖租户已改的 label/sort/color
- 不改业务状态机与 DB CHECK

验收按卡片 §3 十二条逐条贴证据，第 2、3 条要贴租户字典数量对比。
完成后停下报告，不要做前端。
```

## 6. 后端验收记录（2026-11-15）

分支 `feat/be-idn-008-dict-supply` 从 `c3c5549` 创建。没有新增依赖、迁移、HTTP 路由或业务状态变更。
前端审查报告 §2 已只读核查；其中旧值并存/v2 的建议以本卡后续决定为准，不实现旧值兼容。

### 6.1 十二项逐条证据

| # | 结果 | 命令 / 测试 / 实测证据 |
|---|---|---|
| 1 | 通过 | `TestSystemDictSupplyIntegrationAdminValues`：GET admin 列表 total=9（本卡 8 类 + user_status）；逐类 GET items 与代码期望值完全一致，scope=system，label/color 非空，sort 从 1 递增。开发库只读复查也是 9 类/33 项。 |
| 2 | 通过 | `TestIdentityDictSupplyIntegrationProvision` 通过真实 POST `/admin/tenants` 开通隔离测试库的临时租户，再查持久化结果：`Provision tenant=2: default dictionaries=9, new tenant dictionaries=9 items=33`。 |
| 3 | 通过（全部现存租户） | 实际 `go run ./cmd/cli dict-reset` 输出见 §6.2：12 个现存租户全部到 9 类/33 项。租户 5、6 不在开发库中，未伪造；`TestSystemDictCLIIntegrationResetAllTenants` 在隔离库另覆盖 1–14，含 suspended 的 tenant 14。 |
| 4 | 通过 | `TestSystemDictSupplyIntegrationSyncCheckDomain` 将迁移 000006 CHECK、真实数据库 CHECK、种子 5 值逐字比较；`TestSystemDictSupplyIntegrationOrderDisplayDomain` 逐条执行真实 orderDisplaySQL，并对 6×3×3 组输入的输出集合做核对。逐值记录见 §6.3。 |
| 5 | 通过 | reset 后开发库只读查询：`obsolete_sync_and_order_items=0`。删除的是 sync 的 pending/succeeded、order 的 paid/cancelled；sync 的 cancelled 属合法新值，仍保留。隔离 CLI 测试同样断言旧值计数为 0。 |
| 6 | 通过 | `TestSystemDictCLIIntegrationBackfillPreservesBusinessEdits`：业务 delivery_mode/courier 的 `Tenant business label`/42/purple，在连续两次 seed+backfill 后整份项快照不变；system 的定制 label/sort/color/status/extra 也不变。 |
| 7 | 通过 | `TestSystemDictSupplyIntegrationProductMeta`：GET `/meta/dicts/product_status` HTTP 200，值为 draft/active/archived，3 项均有 color 和 scope=system，响应通过现有 OpenAPI 校验。 |
| 8 | 通过 | `TestSystemDictionaryWritableFields` + `TestSystemDictionaryServiceUpdateWhitelist` 逐字段验证允许集与拒绝集；HTTP 集成测试另覆盖 type/value/scope/builtin/future 拒绝，system 加项=400、删项/删类型=409，修改失败后对象完全不变。输出见 §6.4。 |
| 9 | 后端通过；页面未通过验收 | `TestSystemDictSupplyIntegrationDisabledUserStatusManagement`：停用后 meta=[]，管理列表/项列表/改项/重新启用均 HTTP 200。前端 dictionary-form.vue、item-form.vue 仍使用 `useMetaDictionary('user_status')` 且无本地 options 兜底；按“不要做前端”未修改，不能声称页面自举已完成。 |
| 10 | 通过 | CLI 隔离测试连续两次 seed+backfill：租户 1 已有项全量快照不变，租户 2 数量 0/0 → 9/33 → 9/33；两次 reset 后 14 个租户均保持 9/33。开发库 reset 后再 backfill，12 个租户均输出 `dicts=9->9 items=33->33`。 |
| 11 | 后端通过；前端报告状态待更新 | `docs/design/dictionary-usage.md` 已登记当前集合、消费清单、CLI 与自举边界；所有 HTTP 集成响应使用原 OpenAPI 校验。前端仓库 `docs/reports/system-dictionary-2026-11-15/report.md` 未改，仍是审查时快照，状态由后续前端任务更新。 |
| 12 | 通过 | `powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1`：go vet、golangci-lint、go test 全通过，最后一行 `all checks passed`。bootstrap=384.775s、cmd/cli=43.822s、system=7.429s、kernel/db=14.751s；无失败。完整日志 `%TEMP%/BE-IDN-008-verify.log`。 |

### 6.2 开发库实际重置与回填

加载既有 CH_* 配置后执行 `go run ./cmd/cli dict-reset`，未打印连接配置：

```text
tenant=1 dicts=3->9 items=13->33
tenant=2 dicts=0->9 items=0->33
tenant=3 dicts=0->9 items=0->33
tenant=4 dicts=0->9 items=0->33
tenant=7 dicts=0->9 items=0->33
tenant=8 dicts=0->9 items=0->33
tenant=9 dicts=0->9 items=0->33
tenant=10 dicts=0->9 items=0->33
tenant=11 dicts=0->9 items=0->33
tenant=12 dicts=0->9 items=0->33
tenant=13 dicts=0->9 items=0->33
tenant=14 dicts=0->9 items=0->33
dict-reset completed tenants=12
```

随后 `go run ./cmd/cli dict-backfill`：上述 12 个租户逐个输出 `dicts=9->9 items=33->33`，
最后一行 `dict-backfill completed tenants=12`。只读 SQL 复查 9 类全部 scope=system，33 项全部 color 非空。

### 6.3 值域逐值对照

| 序号 | sync_run_status | 000006 CHECK | 运行数据库 CHECK |
|---|---|---|---|
| 1 | running | running | running |
| 2 | success | success | success |
| 3 | partial | partial | partial |
| 4 | failed | failed | failed |
| 5 | cancelled | cancelled | cancelled |

| 序号 | lifecycle / payment / after_sales 输入 | orderDisplaySQL 实际输出 | order_status |
|---|---|---|---|
| 1 | pending_payment / unpaid / none | pending_payment | pending_payment |
| 2 | paid / paid / none | pending_shipment | pending_shipment |
| 3 | shipped / paid / none | shipped | shipped |
| 4 | completed / paid / none | completed | completed |
| 5 | cancelled / unpaid / none | closed | closed |
| 6 | paid / paid / refunding | refunding | refunding |
| 7 | cancelled / refunded / refunded | refunded | refunded |
| 8 | future_state / unpaid / none | unknown | unknown |

future_state 仅是 SQL 投影的兜底测试输入，不写入任何业务表或放宽 CHECK。

### 6.4 白名单逐字段输出

```text
PASS TestSystemDictionaryWritableFields/dictionary/name
PASS TestSystemDictionaryWritableFields/dictionary/status
PASS TestSystemDictionaryWritableFields/dictionary/type       rejected unlisted field
PASS TestSystemDictionaryWritableFields/dictionary/scope      rejected unlisted field
PASS TestSystemDictionaryWritableFields/dictionary/builtin    rejected unlisted field
PASS TestSystemDictionaryWritableFields/item/label
PASS TestSystemDictionaryWritableFields/item/sort
PASS TestSystemDictionaryWritableFields/item/color
PASS TestSystemDictionaryWritableFields/item/status
PASS TestSystemDictionaryWritableFields/item/extra
PASS TestSystemDictionaryWritableFields/item/value            rejected unlisted field
PASS TestSystemDictionaryWritableFields/item/builtin          rejected unlisted field
PASS TestSystemDictionaryWritableFields/future_field_defaults_to_rejected
PASS TestSystemDictionaryServiceUpdateWhitelist
PUT dictionary field=type rejected HTTP 409
PUT dictionary field=value/scope/builtin/future each rejected HTTP 400
PUT item field=value rejected HTTP 409
PUT item field=type/scope/builtin/future each rejected HTTP 400
```

id/tenant_id/dict_id/created_at/updated_at 等非展示字段也逐项覆盖拒绝；HTTP 请求格式和错误码没有变化。

### 6.5 附加回归与检查

- `TestSystemDictResetRejectsProductionBeforeOpeningDatabase`：prod、PROD、带空格的 prod 均返回 `dict-reset is forbidden in prod`，无需数据库连接。
- CI 分组守卫：`powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-test-groups.ps1`；`Source 546 = shards 545 + excluded 1`，`PASS: every integration test has exactly one shard or an explicit exclusion.`
- `gofmt -l $dictSupplyFiles` 本次 18 个 Go 文件无输出；`go run "$env:TEMP/BE-IDN-008-sizecheck.go" -- @dictSupplyFiles` 使用 Go parser 检查：`changed Go files max=238 lines; functions max=52 lines`。`$dictSupplyFiles` 是 `git diff --name-only -- '*.go'` 与 `git ls-files --others --exclude-standard -- '*.go'` 的并集。
- `rg -n 'TODO|not implemented' internal/` 无命中；新增生产代码无 fmt.Print/log.Print/slog 调试输出或 context.Background。CLI 输出仅租户 ID、计数、命令结果；新增错误对外不含原始数据库错误，保留 Cause 供排查。
- `git diff --check` 通过；未修改已合并迁移、业务状态机、CHECK、依赖版本或前端文件。

专项验收的实际命令和汇总（完整逐字段输出存于 `%TEMP%/BE-IDN-008-acceptance.log`）：

```powershell
go test -count=1 -v ./internal/modules/system ./internal/bootstrap ./cmd/cli -run '^(TestSystemDictionary(WritableFields|ServiceUpdateWhitelist)|TestSystemDictSupplyIntegration.*|TestIdentityDictSupplyIntegrationProvision|TestSystemDictCLIIntegration.*|TestSystemDictResetRejectsProductionBeforeOpeningDatabase)$'
```

```text
ok  github.com/omwk/go-admin-api/internal/modules/system  0.128s
ok  github.com/omwk/go-admin-api/internal/bootstrap       4.432s
ok  github.com/omwk/go-admin-api/cmd/cli                  9.137s
```

迁移全链、并发启动、结构不变量，以及 Provision/reset 失败回滚均通过；
逐项输出在 `%TEMP%/BE-IDN-008-transaction-evidence.log`。其中
`TestMigrateIntegrationReversibleAndRepeatable` 执行完整 up、down 到版本 0、再次 up，
没有在开发业务数据库执行破坏性 down。可复跑命令：

```powershell
go test -count=1 -v ./internal/kernel/db -run '^(TestMigrateIntegration(ReversibleAndRepeatable|ConcurrentStartup)|TestMigrationIntegration(PartitionInvariants|TenantAndIdempotencyUniqueness|VersionDefaultsAndCompositeKeys))$'
```

```text
PASS TestMigrateIntegrationReversibleAndRepeatable (4.96s)
PASS TestMigrateIntegrationConcurrentStartup (1.60s)
PASS TestMigrationIntegrationPartitionInvariants (0.49s)
PASS TestMigrationIntegrationTenantAndIdempotencyUniqueness (0.12s)
PASS TestMigrationIntegrationVersionDefaultsAndCompositeKeys (0.13s)
ok  github.com/omwk/go-admin-api/internal/kernel/db 7.506s
```

本机无 make，`verify.ps1` 直接调用与 Makefile 对应的 lint/test 命令；没有把未执行的
`make lint` / `make test` 或本地 race 写成已通过。此次没有新增 HTTP 接口，接口表不需增加。

### 6.6 偏差与遗留

- 按本次任务边界只实现后端，第 9 条页面本地兜底和第 11 条前端审查报告状态未交付，留待前端任务；未通过部分没有勾成完成。
- 开发库只存在 12 个租户，5、6 不存在；全覆盖以现存租户为准，2–14 的完整编号范围另由隔离集成测试验证。
- 本机无 gcc、CGO_ENABLED=0，未在本地运行 race；合并前仍需查看 CI 的 race 结果。本次不合并 PR、不做前端。
