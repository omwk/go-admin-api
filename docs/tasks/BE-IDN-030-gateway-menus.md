# BE-IDN-030 · 开放平台菜单与初始路由

- ID: BE-IDN-030
- 类型: 新增
- 仓库: 后端
- 状态: 已实现（本地验收通过）
- 预计: 0.3 人日
- 分支: `feat/be-idn-030-gateway-menus`
- 依赖: BE-IDN-029（观测接口已合并）

---

## 1. 问题

前端要建「开放平台」控制台，但后端没有对应菜单，侧边栏无法进入；
且 `api_route` 初始为空，试调功能没有任何可调的路由。

## 2. 改动

### 2.1 菜单

新增一级目录与四个页面（component 用简写，与 FE-IDN-018 一致）：

```
开放平台 (dir, /open-platform, sort 5)
├ API 客户端  /open-platform/clients   open-platform/clients/index  gateway:client:list   sort 1
├ 路由        /open-platform/routes    open-platform/routes/index   gateway:route:list    sort 2
├ 调用日志    /open-platform/calls     open-platform/calls/index    gateway:call:read     sort 3
└ 用量统计    /open-platform/usage     open-platform/usage/index    gateway:call:read     sort 4
```

- 写入 `seed_pages` 覆盖守卫清单
- 权限码已存在（BE-IDN-029 补齐），无需新增

### 2.2 初始路由（seed）

插入两条内置路由（`tenant_id = 平台租户`）：

| code | name | method | path | visibility | billing_mode | capability |
|---|---|---|---|---|---|---|
| `usage.read` | 用量查询 | GET | `/open/v1/usage` | public | free | `usage.read` |

- 幂等：按 `code` upsert，已存在不覆盖租户修改
- 只 seed 这一条，其他路由由管理员在界面添加

### 2.3 文档

`docs/design/api-gateway-console.md` 补一节「如何新增一个对外 API」：

```
1. 后端实现 /open/v1/xxx 处理器并注册
2. 管理端「路由」新建：code、方法、路径、计费模式
3. 给客户端授权并设配额
4. 用「试调」验证
```

## 3. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 菜单可见 | 超管 `/auth/menus` 含「开放平台」下四项 |
| 2 | 权限过滤 | 无 `gateway:route:list` 的账号看不到路由菜单 |
| 3 | 覆盖守卫 | 从 seed_pages 删除任一菜单 → 测试失败 |
| 4 | 初始路由 | 新库 seed 后 `api_route` 含 `usage.read`；重复 seed 不重复 |
| 5 | 可试调 | 用「试调」调用 `usage.read` 成功返回用量 |
| 6 | 全量 | `verify` 通过 |

## 4. Codex prompt

```
实现 docs/tasks/BE-IDN-030-gateway-menus.md。

分支：feat/be-idn-030-gateway-menus（从最新 main d2531bb 开）

必读：本卡、internal/modules/identity/seed_catalog.go、
seed_pages_test.go、internal/modules/gateway/admin_http.go（route 字段）、
migrations/（api_route）

改动：
1. 新增「开放平台」目录与四个页面菜单（简写 component），写入覆盖清单
2. seed 一条内置路由 usage.read（GET /open/v1/usage，public，free），幂等
3. 设计文档补「如何新增一个对外 API」
4. OpenAPI 无变化

约束：
- 不硬编码租户 ID，平台租户从配置读取
- 不覆盖租户对内置路由的修改（已存在则跳过）

验收按卡片 §3 六条逐条贴证据；第 5 条贴试调成功响应。
完成后停下报告，不要做前端。
```

## 5. 实现与验证计划

- [x] 在 seed catalog 和独立页面清单加入开放平台目录及四个页面，验证删除页面时守卫失败。
- [x] CLI 从 `tenant.platform_id` 传入平台租户，新增 `usage.read` seed；使用 `(tenant_id,code) ON CONFLICT DO NOTHING` 保留已有记录。
- [x] 补齐已有用量处理器的在线试调支持，验证真实签名、授权、配额与调用日志，保持 API / OpenAPI 契约。
- [x] 设计文档补充新增对外 API 的操作步骤。
- [x] 在隔离测试数据库逐条验证 §3，保存成功试调响应，运行 verify 和测试分片守卫后报告。

## 6. 本地验收记录（2026-11-24）

- 分支：`feat/be-idn-030-gateway-menus`；基线：`de521fb`（开工时 fetch 后的 `origin/main`）。
- PostgreSQL 使用现有测试设施的隔离数据库／schema；Redis 使用 miniredis。试调固定测试时钟为 `2027-01-15T08:00:00Z`，实际经过管理端 JWT/RBAC → 服务端 HMAC v2 签名 → open 处理器 → 用量查询与调用日志。

| §3 | 验收证据 | 结果 |
|---|---|---|
| 1 菜单可见 | `TestSeedGatewayMenusIntegrationVisibilityAndPermissions`：超管 `GET /api/v1/auth/menus` 返回「开放平台」sort=5，四页分别为 clients/routes/calls/usage，sort=1/2/3/4，component 均为简写。完整树见 [acceptance-tests.txt](evidence/BE-IDN-030/acceptance-tests.txt)。 | 通过 |
| 2 权限过滤 | 同一测试为普通账号授予全部五项菜单，但仅授予 `gateway:client:list`、`gateway:call:read`；响应仅包含 clients/calls/usage，不含 routes。 | 通过 |
| 3 覆盖守卫 | 使用 Go `-overlay` 分别删除目录及四个页面；五次 `TestMenuSeedCoversDeclaredPages` 均 exit=1，报告 missing preceding parent / missing declared page；原始代码 exit=0。见 [coverage-mutations.txt](evidence/BE-IDN-030/coverage-mutations.txt)。 | 通过 |
| 4 初始路由 | `TestSeedCLIIntegrationGatewayRouteIdempotency`：新库实际 CLI seed 后仅一条 usage.read，重复 seed 全字段及时间戳不变；修改 name/visibility/status/billing/price/units/version/created_by 后再次 seed 保持原样。`TestSeedCLIIntegrationGatewayRouteConfiguredTenant`：环境变量设为 23 后仅补入租户 23，租户 1 原记录保持不变，再次执行行数不变。 | 通过 |
| 5 可试调 | `TestGatewaySeedIntegrationUsageTrial`：`POST /api/v1/admin/api-clients/1/try`，route_id=2，GET `/open/v1/usage` → HTTP 200 / code=0 / status_code=200；完整请求、响应见下。 | 通过 |
| 6 全量 | `powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1` → exit=0，末行 `all checks passed`；包含 go vet、golangci-lint、go test ./...。见 [verify.txt](evidence/BE-IDN-030/verify.txt)。 | 通过 |

聚焦验收命令（完整输出见 [acceptance-tests.txt](evidence/BE-IDN-030/acceptance-tests.txt)）：

```powershell
go test -count=1 -v ./internal/modules/identity ./cmd/cli ./internal/bootstrap -run '^(TestMenuSeed.*|TestSeedCLIIntegrationGatewayRoute.*|TestSeedGatewayMenusIntegration.*|TestGatewaySeedIntegration.*|TestSeedMenuIntegration.*)$'
```

### 第 5 项试调请求与成功响应

请求：[trial-request.json](evidence/BE-IDN-030/trial-request.json)；完整原始响应：[trial-response.json](evidence/BE-IDN-030/trial-response.json)。认证信息未记录。

```http
POST /api/v1/admin/api-clients/1/try
Content-Type: application/json

{"route_id":2,"method":"GET","path":"/open/v1/usage","body":null}
```

```json
{"code":0,"message":"ok","data":{"status_code":200,"error_code":0,"latency_ms":55,"response":{"as_of":"2027-01-15T08:00:00Z","calls":1,"date":"2027-01-15","failed_calls":0,"metered_calls":0,"processing_calls":1,"succeeded_calls":0},"truncated":false,"test":true,"call_id":"01M8555Y5QV73JMEDGVSVT35Q2","billing":{"mode":"free","units":1,"billed":false}},"trace_id":"01M8555Y4V3X79BD3G0S1X9XMT"}
```

`calls=1 / processing_calls=1` 是本次试调在读取用量前登记的 processing 日志；响应后该日志完成为 succeeded。断言 `charge_source=manual_test`、持久化 result=`{}`；`api_call_meter / call_charge / wallet_txn / unit_txn / api_quota` 均为 0 行。

门禁回归：未授权 403/80104、路由停用／私有 404/80201、客户端停用 401/80101、密钥撤销 401/80102、路径越界／非法日期 400、日／月配额耗尽 429/10005；试调不改变 admitted_count。

### 补充自检与实现说明

- 迁移：`go test -count=1 -v ./internal/kernel/db -run '^TestMigrateIntegrationReversibleAndRepeatable$'` 通过，执行全链 up、重复 up、down 到 0、检查表已清空、重新 up；见 [migrations.txt](evidence/BE-IDN-030/migrations.txt)。
- 分片守卫：`Source 670 = shards 669 + excluded 1`；每个集成测试恰好归属一个分片；见 [test-groups.txt](evidence/BE-IDN-030/test-groups.txt)。
- 权限投影守卫：移除 `gateway:client:list / gateway:route:list / gateway:call:read` 三项已过期的豁免，原断言全部保留；`registered=123 projected=24 exempt=99`，测试通过。见 [permission-projection.txt](evidence/BE-IDN-030/permission-projection.txt)。
- 源码检查：13 个改动 Go 文件，最大文件 213 行、最大函数 50 行；`rg -n 'TODO|not implemented' internal` 无匹配；`git diff --check` 通过；OpenAPI、go.mod/go.sum、migrations 相对 de521fb 无改动。见 [source-checks.txt](evidence/BE-IDN-030/source-checks.txt)。
- 日志检查：生产改动未增加日志输出；新增测试仅记录菜单树、用量与公开试调响应，不记录密码、密钥或 bearer。
- 为落实 §3.2，菜单投影同时校验菜单授权和页面权限码；为落实 §3.5，将已有 usage.read 处理器登记到能力清单，并补入真实试调的门禁、配额检查与仅元数据落库。接口和 OpenAPI 契约不变。
- 已清理：上述聚焦测试的 `t.Cleanup` 删除隔离数据库／schema；本卡验收数据 0 保留，开发库无新增验收记录。

完整回归首轮 bootstrap 包通过（445.859s），发现并清理上述旧权限豁免后重新执行 verify，末行 `all checks passed`；最终 identity 包 24.041s，其余成功结果按 Go 的有效测试缓存复用。首轮记录保留在 [verify-first-attempt.txt](evidence/BE-IDN-030/verify-first-attempt.txt)。

偏差说明：无。未改前端；未新增 API、OpenAPI、依赖或数据库迁移。
